package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	skill "github.com/leonidas1712/aboard/skills/aboard"
)

// hookSpec is one delivery hook a harness runs.
type hookSpec struct {
	// event is the harness's name for the hook event.
	event string
	// arg is the event argument of "aboard hook <harness> <arg>".
	arg     string
	handler hookHandler
}

// hookHandler is one hook command in a harness's hook settings.
type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
	// AsyncRewake runs a Claude Code hook in the background; exit code 2 wakes the
	// session with the hook's standard error.
	AsyncRewake bool `json:"asyncRewake,omitempty"`
	// AdditionalContextLimit is how much context, in approximate tokens, a Codex hook
	// may add before Codex shortens it.
	AdditionalContextLimit int `json:"additionalContextLimit,omitempty"`
}

// codexContextLimit is how much context, in tokens, Codex takes from the tool hook before
// shortening it: well above the 9,000 bytes the daemon adds at one tool boundary.
const codexContextLimit = 8192

// stopHookTimeout is the stop hook's timeout in seconds. The hook waits for as long as
// the session is idle.
const stopHookTimeout = 86400

// claudeBatchSince is the first Claude Code version with the PostToolBatch hook, which
// fires once each batch of tool calls is done, failed ones included.
const claudeBatchSince = "2.1.118"

// claudeHooks returns Claude Code's hooks. With batch, the tool hook runs on
// PostToolBatch; without it, for a Claude Code older than claudeBatchSince, on
// PostToolUse and PostToolUseFailure, which together fire after every tool call.
func claudeHooks(exe string, batch bool) []hookSpec {
	cmd := func(arg string) string { return shellWord(exe) + " hook claude-code " + arg }
	tool := hookHandler{Type: "command", Command: cmd("tool"), Timeout: 10}
	specs := []hookSpec{
		{"SessionStart", "session-start", hookHandler{Type: "command", Command: cmd("session-start"), Timeout: 30}},
		{"UserPromptSubmit", "prompt", hookHandler{Type: "command", Command: cmd("prompt"), Timeout: 10}},
		{"Stop", "stop", hookHandler{Type: "command", Command: cmd("stop"), Timeout: stopHookTimeout, AsyncRewake: true}},
	}
	if batch {
		specs = append(specs, hookSpec{"PostToolBatch", "tool", tool})
	} else {
		specs = append(specs, hookSpec{"PostToolUse", "tool", tool}, hookSpec{"PostToolUseFailure", "tool", tool})
	}
	return append(specs, hookSpec{"SessionEnd", "end", hookHandler{Type: "command", Command: cmd("end"), Timeout: 10}})
}

// claudeHasBatch reports whether the Claude Code on the PATH has the PostToolBatch hook:
// true when its version is claudeBatchSince or later, or can't be read, since a Claude
// Code installed later will be a current one.
func (a *app) claudeHasBatch(ctx context.Context) bool {
	if a.claudeBatch == nil {
		v := ""
		if path := a.lookPath("claude"); path != "" {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			cmd := exec.CommandContext(cctx, path, "--version") //nolint:gosec // the claude on this command's PATH
			cmd.Env = append(os.Environ(), "HOME="+a.env.Getenv("HOME"), "PATH="+a.env.Getenv("PATH"))
			if out, err := cmd.Output(); err == nil {
				v = string(out)
			}
			cancel()
		}
		batch := versionAtLeast(v, claudeBatchSince)
		a.claudeBatch = &batch
	}
	return *a.claudeBatch
}

// lookPath finds a program on this command's PATH, or returns "".
func (a *app) lookPath(name string) string {
	for _, dir := range filepath.SplitList(a.env.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// versionAtLeast reports whether the first dotted version number in text, such as
// "2.1.288 (Claude Code)", is at least min. Text without one counts as new enough.
func versionAtLeast(text, minimum string) bool {
	found := versionNumber.FindString(text)
	if found == "" {
		return true
	}
	have, want := strings.Split(found, "."), strings.Split(minimum, ".")
	for i := range want {
		h, w := 0, 0
		if i < len(have) {
			h, _ = strconv.Atoi(have[i])
		}
		w, _ = strconv.Atoi(want[i])
		if h != w {
			return h > w
		}
	}
	return true
}

var versionNumber = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

func codexHooks(exe string) []hookSpec {
	cmd := func(arg string) string { return shellWord(exe) + " hook codex " + arg }
	return []hookSpec{
		{"SessionStart", "session-start", hookHandler{Type: "command", Command: cmd("session-start"), Timeout: 30}},
		// The prompt and stop hooks mark when a turn runs, so the owner's messages go to the
		// next tool call rather than the queue, which holds them until the turn ends.
		{"UserPromptSubmit", "prompt", hookHandler{Type: "command", Command: cmd("prompt"), Timeout: 10}},
		{"Stop", "stop", hookHandler{Type: "command", Command: cmd("stop"), Timeout: 10}},
		// Before each tool call, so failed tool calls don't keep the owner's messages out of
		// the turn: Codex runs PostToolUse only after a tool succeeds. It never denies.
		{"PreToolUse", "tool", hookHandler{Type: "command", Command: cmd("tool"), Timeout: 30, AdditionalContextLimit: codexContextLimit}},
		// Codex caps SessionEnd hooks at 3 seconds; closing a session is one socket call.
		{"SessionEnd", "end", hookHandler{Type: "command", Command: cmd("end"), Timeout: 3}},
	}
}

// withHome makes each hook command set ABOARD_HOME when this command runs with it set,
// so hooks use the same folder as the commands that installed them. Codex doesn't pass
// the environment it was started with to its hooks.
func (a *app) withHome(specs []hookSpec) []hookSpec {
	p, err := a.paths()
	if err != nil || p.home == "" {
		return specs
	}
	out := slices.Clone(specs)
	for i := range out {
		out[i].handler.Command = "ABOARD_HOME=" + shellWord(p.home) + " " + out[i].handler.Command
	}
	return out
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// shellWord quotes s for a shell command line when it needs quoting.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isAboardHook reports whether a hook command runs "aboard hook <harness> <arg>".
func isAboardHook(command, harness, arg string) bool {
	return strings.Contains(command, "aboard") && strings.HasSuffix(strings.TrimSpace(command), " hook "+harness+" "+arg)
}

// aboardHookArg returns the <arg> of a command that runs "aboard hook <harness> <arg>".
func aboardHookArg(command, harness string) (string, bool) {
	if !strings.Contains(command, "aboard") {
		return "", false
	}
	fields := strings.Fields(command)
	n := len(fields)
	if n < 3 || fields[n-3] != "hook" || fields[n-2] != harness {
		return "", false
	}
	return fields[n-1], true
}

// harnessSetup is what aboard init does for one harness.
type harnessSetup struct {
	Name     string       `json:"name"`
	Detected bool         `json:"detected"`
	Changes  []fileChange `json:"changes"`
	// otherScope is set when the other scope already holds the harness's hooks.
	otherScope bool
}

// fileChange is one file aboard init writes.
type fileChange struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	// Allow lists the allow rules for aboard the file holds, when they were asked for.
	Allow []string `json:"allow,omitempty"`
	data  []byte
	perm  os.FileMode
	// commands are the hook commands the file runs, shown in text output.
	commands []string
}

// File change actions.
const (
	actionCreate    = "create"
	actionUpdate    = "update"
	actionUnchanged = "unchanged"
)

// runInit adds the Aboard skill and the delivery hooks to Claude Code and Codex, for
// every project or for this one. In a terminal it asks what to set up and confirms
// before writing; otherwise it writes only with --yes and else lists the changes.
func runInit(ctx context.Context, a *app, args []string) error {
	const use = "aboard init [--yes] [--scope global|project] [--harness H[,H]] [--delivery auto|humans|off] [--allow-commands] [--json]"
	flags := a.flags("init")
	yes := flags.Bool("yes", false, "make the changes without asking")
	scope := flags.String("scope", scopeGlobal, "global: every project (under your home directory); project: only this directory")
	var harnesses listFlag
	flags.Var(&harnesses, "harness", "the harnesses to set up, comma separated (default: every one found)")
	mode := flags.String("delivery", "", "the delivery mode for agents on this machine without their own: auto, humans or off")
	allow := flags.Bool("allow-commands", false, "let agents run aboard commands without a permission prompt")
	if _, err := a.parse(flags, args, use, 0, 0); err != nil {
		return err
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	c := initChoices{scope: *scope, harnesses: harnesses, delivery: delivery.Mode(*mode), allow: *allow}
	if err := a.checkInitChoices(c, use); err != nil {
		return err
	}
	home := a.env.Getenv("HOME")
	if home == "" {
		return homeError(errors.New("HOME is not set"))
	}
	exe, err := a.env.Executable()
	if err != nil {
		return fmt.Errorf("find the aboard binary for the hook commands: %w", err)
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	known := []harnessSetup{
		{Name: "claude-code", Detected: detected(a.configDir("claude-code"), "claude")},
		{Name: "codex", Detected: detected(a.configDir("codex"), "codex")},
	}
	interactive := a.env.Terminal && !*yes && !a.json
	current := delivery.ModeAuto
	if interactive || c.delivery != "" {
		// The empty AgentRef holds the mode of agents without their own.
		if current, err = a.deliveryMode(ctx, delivery.AgentRef{}); err != nil {
			return err
		}
	}
	p := &prompter{in: bufio.NewReader(a.env.Stdin), out: a.env.Stdout}
	if interactive {
		var found []string
		for _, h := range known {
			if h.Detected {
				found = append(found, h.Name)
			}
		}
		a.askInit(p, &c, found, set, current)
		if err := a.checkInitChoices(c, use); err != nil {
			return err
		}
	}

	setups, err := a.planInit(ctx, c, known, exe)
	if err != nil {
		return err
	}
	var modeChange *initDelivery
	if c.delivery != "" {
		modeChange = &initDelivery{Mode: c.delivery, Action: actionUnchanged}
		if c.delivery != current {
			modeChange.Action = actionUpdate
		}
	}
	apply := *yes
	list, pending := initList(setups, modeChange, c, home)
	if interactive {
		_, _ = io.WriteString(a.env.Stdout, "\n"+list)
		if pending == 0 {
			_, _ = io.WriteString(a.env.Stdout, initEnding(c, nil, 0, false))
			return nil
		}
		if apply = p.yes("Make these changes?"); !apply {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		// The changes are already on screen; say only how it ended.
		list = ""
	}
	if apply {
		if err := a.applyInit(ctx, setups, modeChange); err != nil {
			return err
		}
	}
	a.emit(struct {
		Applied       bool           `json:"applied"`
		Scope         string         `json:"scope"`
		Delivery      *initDelivery  `json:"delivery"`
		AllowCommands bool           `json:"allow_commands"`
		Harnesses     []harnessSetup `json:"harnesses"`
	}{apply, c.scope, modeChange, c.allow, setups}, list+a.codexAllowLine(c, setups, interactive)+initEnding(c, setups, pending, apply))
	return nil
}

// initDelivery is the delivery mode aboard init sets for agents without their own.
type initDelivery struct {
	Mode   delivery.Mode `json:"mode"`
	Action string        `json:"action"`
}

// checkInitChoices rejects choices aboard init can't act on.
func (a *app) checkInitChoices(c initChoices, use string) error {
	if c.scope != scopeGlobal && c.scope != scopeProject {
		return usageError(fmt.Sprintf("%q is not a scope; use global or project.", c.scope), use)
	}
	if c.scope == scopeProject && a.env.Dir == a.env.Getenv("HOME") {
		return newError("invalid_request", "This directory is your home directory, so a project setup would be the same as setting up everywhere.",
			"Run aboard init in the project's directory, or use --scope global.")
	}
	for _, h := range c.harnesses {
		if h != "claude-code" && h != "codex" {
			return usageError(fmt.Sprintf("%q is not a harness aboard init sets up; use claude-code or codex.", h), use)
		}
	}
	if c.delivery == "" {
		return nil
	}
	if _, ok := delivery.ParseMode(string(c.delivery)); !ok {
		return usageError(fmt.Sprintf("%q is not a delivery mode; use auto, humans or off.", c.delivery), use)
	}
	return a.refuseInSession("Changing the delivery mode", "aboard init --delivery "+string(c.delivery))
}

// planInit works out every file change for the chosen harnesses, without writing.
func (a *app) planInit(ctx context.Context, c initChoices, known []harnessSetup, exe string) ([]harnessSetup, error) {
	specs := map[string][]hookSpec{"claude-code": a.withHome(claudeHooks(exe, a.claudeHasBatch(ctx))), "codex": a.withHome(codexHooks(exe))}
	setups := slices.Clone(known)
	for i := range setups {
		s := &setups[i]
		s.Changes = []fileChange{}
		if !s.Detected || (c.harnesses != nil && !slices.Contains(c.harnesses, s.Name)) {
			continue
		}
		files := a.setupFiles(s.Name, c.scope)
		sk, err := skillChange(files.skill)
		if err != nil {
			return nil, err
		}
		hk, err := hooksChange(files.hooks, s.Name, specs[s.Name])
		if err != nil {
			return nil, err
		}
		s.Changes = append(s.Changes, sk, hk)
		if scopes, _, err := a.installedScopes(s.Name, specs[s.Name]); err == nil {
			s.otherScope = slices.ContainsFunc(scopes, func(sc string) bool { return sc != c.scope })
		}
		if !c.allow {
			continue
		}
		if s.Name == "claude-code" {
			if err := allowSettings(&s.Changes[1]); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := rulesChange(files.allow)
		if err != nil {
			return nil, err
		}
		s.Changes = append(s.Changes, rc)
	}
	return setups, nil
}

// applyInit writes the planned files and sets the delivery mode.
func (a *app) applyInit(ctx context.Context, setups []harnessSetup, mode *initDelivery) error {
	for _, s := range setups {
		for _, c := range s.Changes {
			if c.Action == actionUnchanged {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(c.Path), 0o750); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(c.Path), err)
			}
			if err := writeFileAtomic(c.Path, c.data, c.perm); err != nil {
				return err
			}
		}
	}
	if mode == nil || mode.Action == actionUnchanged {
		return nil
	}
	if _, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpMode, Agent: &delivery.AgentRef{}, Mode: mode.Mode}); err != nil {
		return err
	}
	return nil
}

// detected reports whether a harness looks installed: its folder exists or its command
// is on the PATH.
func detected(dir, command string) bool {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return true
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func skillChange(path string) (fileChange, error) {
	c := fileChange{Path: path, Kind: "skill", Action: actionCreate, data: skill.Skill, perm: 0o644}
	old, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", path, err)
	case bytes.Equal(old, skill.Skill):
		c.Action = actionUnchanged
	default:
		c.Action = actionUpdate
	}
	return c, nil
}

func hooksChange(path, harness string, specs []hookSpec) (fileChange, error) {
	c := fileChange{Path: path, Kind: "hooks", Action: actionCreate, perm: 0o644}
	for _, s := range specs {
		c.commands = append(c.commands, s.event+": "+s.handler.Command)
	}
	old, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", path, err)
	default:
		c.Action = actionUpdate
		if info, err := os.Stat(path); err == nil {
			c.perm = info.Mode().Perm()
		}
	}
	data, changed, err := mergeHooks(old, harness, specs)
	if err != nil {
		return c, &Error{
			Code: "invalid_request", Message: "Couldn't read the hook settings in " + path + ": " + err.Error(),
			Hint: "Fix the file so it is a JSON object, then run aboard init again.", Err: err,
		}
	}
	if !changed && c.Action == actionUpdate {
		c.Action = actionUnchanged
	}
	c.data = data
	return c, nil
}

// mergeHooks adds the delivery hooks to a harness's hook settings, keeping every other
// setting and hook as it was. An Aboard hook already there is updated in place when its
// command or options differ. changed is false when nothing needs writing.
func mergeHooks(data []byte, harness string, specs []hookSpec) (out []byte, changed bool, err error) {
	root, err := parseJSONObject(data)
	if err != nil {
		return nil, false, err
	}
	hooks := newJSONObject()
	if raw, ok := root.get("hooks"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if hooks, err = parseJSONObject(raw); err != nil {
			return nil, false, fmt.Errorf("hooks: %w", err)
		}
	}
	for _, spec := range specs {
		var groups []json.RawMessage
		if raw, ok := hooks.get(spec.event); ok && string(bytes.TrimSpace(raw)) != "null" {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, false, fmt.Errorf("hooks.%s: %w", spec.event, err)
			}
		}
		want, err := json.Marshal(spec.handler)
		if err != nil {
			return nil, false, fmt.Errorf("encode hook: %w", err)
		}
		found, eventChanged := false, false
		for gi, g := range groups {
			group, err := parseJSONObject(g)
			if err != nil {
				return nil, false, fmt.Errorf("hooks.%s: %w", spec.event, err)
			}
			var handlers []json.RawMessage
			if raw, ok := group.get("hooks"); ok {
				if err := json.Unmarshal(raw, &handlers); err != nil {
					return nil, false, fmt.Errorf("hooks.%s: %w", spec.event, err)
				}
			}
			groupChanged := false
			for hi, h := range handlers {
				var have map[string]any
				if json.Unmarshal(h, &have) != nil {
					continue
				}
				command, _ := have["command"].(string)
				if !isAboardHook(command, harness, spec.arg) {
					continue
				}
				found = true
				var wantMap map[string]any
				_ = json.Unmarshal(want, &wantMap)
				if !reflect.DeepEqual(have, wantMap) {
					handlers[hi], groupChanged = want, true
				}
			}
			if groupChanged {
				raw, err := json.Marshal(handlers)
				if err != nil {
					return nil, false, fmt.Errorf("encode hooks: %w", err)
				}
				group.set("hooks", raw)
				if groups[gi], err = json.Marshal(group); err != nil {
					return nil, false, fmt.Errorf("encode hooks: %w", err)
				}
				eventChanged = true
			}
		}
		if !found {
			g, err := json.Marshal(map[string][]json.RawMessage{"hooks": {want}})
			if err != nil {
				return nil, false, fmt.Errorf("encode hooks: %w", err)
			}
			groups = append(groups, g)
			eventChanged = true
		}
		if eventChanged {
			raw, err := json.Marshal(groups)
			if err != nil {
				return nil, false, fmt.Errorf("encode hooks: %w", err)
			}
			hooks.set(spec.event, raw)
			changed = true
		}
	}
	removed, err := removeStaleHooks(hooks, harness, specs)
	if err != nil {
		return nil, false, err
	}
	changed = changed || removed
	if !changed {
		return data, false, nil
	}
	raw, err := json.Marshal(hooks)
	if err != nil {
		return nil, false, fmt.Errorf("encode hooks: %w", err)
	}
	root.set("hooks", raw)
	out, err = encodeIndented(root)
	return out, true, err
}

// initList lists the changes, one file per line, and returns how many are pending.
func initList(setups []harnessSetup, mode *initDelivery, c initChoices, home string) (list string, pending int) {
	var b strings.Builder
	short := func(p string) string {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
		return p
	}
	for _, s := range setups {
		if !s.Detected {
			fmt.Fprintf(&b, "%s: not found on this machine\n", s.Name)
			continue
		}
		if len(s.Changes) == 0 {
			fmt.Fprintf(&b, "%s: not chosen\n", s.Name)
			continue
		}
		fmt.Fprintf(&b, "%s, %s:\n", s.Name, scopeText(c.scope))
		if s.otherScope {
			other := scopeGlobal
			if c.scope == scopeGlobal {
				other = scopeProject
			}
			fmt.Fprintf(&b, "  (its hooks are also set up %s; with both, it may run each hook twice)\n", scopeText(other))
		}
		for _, ch := range s.Changes {
			fmt.Fprintf(&b, "  %-9s %s (%s)\n", ch.Action, short(ch.Path), ch.Kind)
			if ch.Action == actionUnchanged {
				continue
			}
			pending++
			for _, cmd := range ch.commands {
				fmt.Fprintf(&b, "            %s\n", cmd)
			}
			if ch.Kind == "permissions" {
				b.WriteString("            (Codex runs the commands it allows outside its sandbox.)\n")
			}
		}
	}
	if mode != nil {
		fmt.Fprintf(&b, "delivery for agents without their own mode: %s (%s)\n", mode.Mode, mode.Action)
		if mode.Action != actionUnchanged {
			pending++
		}
	}
	return b.String(), pending
}

// codexAllowLine recommends --allow-commands when init sets up Codex without it and no
// rule Codex reads here allows aboard yet. In a terminal the question already said why.
func (a *app) codexAllowLine(c initChoices, setups []harnessSetup, interactive bool) string {
	if c.allow || interactive {
		return ""
	}
	for _, s := range setups {
		if s.Name != "codex" || len(s.Changes) == 0 {
			continue
		}
		for _, scope := range []string{scopeGlobal, c.scope} {
			if _, ok := codexAllows(filepath.Dir(a.setupFiles("codex", scope).allow)); ok {
				return ""
			}
		}
		return codexAllowAdvice
	}
	return ""
}

// harnessTitles are the harnesses' names as people know them.
var harnessTitles = map[string]string{"claude-code": "Claude Code", "codex": "Codex"}

// initEnding says what happened, or what to run to make the changes. Only the harnesses
// whose hook files were added or updated ask the person to trust the hooks again.
func initEnding(c initChoices, setups []harnessSetup, pending int, applied bool) string {
	switch {
	case pending == 0:
		return "Nothing to change.\n"
	case !applied:
		return "Run " + initCommand(c) + " to make these changes.\n"
	}
	var trust []string
	for _, s := range setups {
		for _, ch := range s.Changes {
			if ch.Kind == "hooks" && ch.Action != actionUnchanged {
				trust = append(trust, harnessTitles[s.Name])
				break
			}
		}
	}
	switch len(trust) {
	case 0:
		return "Done.\n"
	case 1:
		return "Done. " + trust[0] + " asks you to trust new hooks before they run: review them in /hooks.\n" + projectEnding(c, trust)
	}
	return "Done. " + strings.Join(trust, " and ") +
		" ask you to trust new hooks before they run: review them in /hooks in each.\n" + projectEnding(c, trust)
}

// projectEnding is what a project setup that changed hook files adds to init's ending.
func projectEnding(c initChoices, trust []string) string {
	if c.scope != scopeProject {
		return ""
	}
	var end string
	if slices.Contains(trust, harnessTitles["codex"]) {
		end = "Codex reads a project's .codex folder only once you trust the project.\n"
	}
	return end + "The hook files name this machine's aboard binary; keep them out of version control.\n"
}

// initCommand is the aboard init command that makes the changes c describes.
func initCommand(c initChoices) string {
	cmd := "aboard init --yes"
	if c.scope != scopeGlobal {
		cmd += " --scope " + c.scope
	}
	if c.harnesses != nil {
		cmd += " --harness " + strings.Join(c.harnesses, ",")
	}
	if c.delivery != "" {
		cmd += " --delivery " + string(c.delivery)
	}
	if c.allow {
		cmd += " --allow-commands"
	}
	return cmd
}

// removeStaleHooks takes out Aboard's hook entries for harness that specs no longer
// have, such as the tool hook on an event this aboard moved it from, and any group or
// event left empty by that. Other hooks are kept as they were.
func removeStaleHooks(hooks *jsonObject, harness string, specs []hookSpec) (bool, error) {
	wanted := map[string]bool{}
	for _, s := range specs {
		wanted[s.event+" "+s.arg] = true
	}
	changed := false
	for _, event := range slices.Clone(hooks.keys) {
		raw, _ := hooks.get(event)
		var groups []json.RawMessage
		if json.Unmarshal(raw, &groups) != nil {
			continue
		}
		var keptGroups []json.RawMessage
		eventChanged := false
		for _, g := range groups {
			group, err := parseJSONObject(g)
			if err != nil {
				keptGroups = append(keptGroups, g)
				continue
			}
			var handlers []json.RawMessage
			if raw, ok := group.get("hooks"); !ok || json.Unmarshal(raw, &handlers) != nil {
				keptGroups = append(keptGroups, g)
				continue
			}
			kept := handlers[:0:0]
			for _, h := range handlers {
				var have struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(h, &have) == nil {
					if arg, ok := aboardHookArg(have.Command, harness); ok && !wanted[event+" "+arg] {
						continue
					}
				}
				kept = append(kept, h)
			}
			if len(kept) == len(handlers) {
				keptGroups = append(keptGroups, g)
				continue
			}
			eventChanged = true
			if len(kept) == 0 {
				continue
			}
			raw, err := json.Marshal(kept)
			if err != nil {
				return false, fmt.Errorf("encode hooks: %w", err)
			}
			group.set("hooks", raw)
			out, err := json.Marshal(group)
			if err != nil {
				return false, fmt.Errorf("encode hooks: %w", err)
			}
			keptGroups = append(keptGroups, out)
		}
		if !eventChanged {
			continue
		}
		changed = true
		if len(keptGroups) == 0 {
			hooks.remove(event)
			continue
		}
		raw, err := json.Marshal(keptGroups)
		if err != nil {
			return false, fmt.Errorf("encode hooks: %w", err)
		}
		hooks.set(event, raw)
	}
	return changed, nil
}
