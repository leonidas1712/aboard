package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// withHome makes each hook command set ABOARD_HOME when this command runs with it set,
// so hooks use the same folder as the commands that installed them. Codex doesn't pass
// the environment it was started with to its hooks.
func (a *app) withHome(specs []harness.Hook) []harness.Hook {
	p, err := a.paths()
	if err != nil || p.home == "" {
		return specs
	}
	out := slices.Clone(specs)
	for i := range out {
		out[i].Handler.Command = "ABOARD_HOME=" + shellWord(p.home) + " " + out[i].Handler.Command
	}
	return out
}

// shellWord quotes s for a shell command line when it needs quoting.
func shellWord(s string) string { return harness.ShellWord(s) }

// isAboardHook reports whether a hook command runs "aboard hook <harness> <arg>".
func isAboardHook(command, name, arg string) bool {
	return strings.Contains(command, "aboard") && strings.HasSuffix(strings.TrimSpace(command), " hook "+name+" "+arg)
}

// aboardHookArg returns the <arg> of a command that runs "aboard hook <harness> <arg>".
func aboardHookArg(command, name string) (string, bool) {
	if !strings.Contains(command, "aboard") {
		return "", false
	}
	fields := strings.Fields(command)
	n := len(fields)
	if n < 3 || fields[n-3] != "hook" || fields[n-2] != name {
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
	// Edited is set when the file, or Aboard's entries in it, changed after an aboard
	// wrote it; WrittenBy is that aboard's version.
	Edited    bool   `json:"edited,omitempty"`
	WrittenBy string `json:"written_by,omitempty"`
	data      []byte
	perm      os.FileMode
	// commands are the hook commands the file runs, shown in text output.
	commands []string
	// note is one line shown under the change, saying what it does in the harness.
	note string
	// allowAdded is set when this change adds Aboard's allow rule to the file.
	allowAdded bool
}

// File change actions.
const (
	actionCreate    = "create"
	actionUpdate    = "update"
	actionUnchanged = "unchanged"
)

// runInit adds the Aboard skill and the delivery hooks to each harness, for every
// project or for this one. In a terminal it asks what to set up and confirms
// before writing; otherwise it writes only with --yes and else lists the changes.
func runInit(ctx context.Context, a *app, args []string) error {
	use := usageOf("init")
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
	var known []harnessSetup
	for _, h := range a.registry() {
		known = append(known, harnessSetup{Name: h.Profile().Harness, Detected: h.Detected(a.henv())})
	}
	interactive := !*yes && a.interactive()
	current := delivery.ModeAuto
	if interactive || c.delivery != "" {
		// The empty AgentRef holds the mode of agents without their own.
		if current, err = a.deliveryMode(ctx, delivery.AgentRef{}); err != nil {
			return err
		}
	}
	if interactive {
		proceed, err := a.askInit(ctx, &c, known, set, current)
		if err != nil || !proceed {
			return err
		}
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
	st := a.out()
	apply := *yes
	list, pending := initList(setups, modeChange, c, home, st, interactive)
	if interactive {
		_, _ = io.WriteString(a.env.Stdout, "\n"+st.heading("Changes")+"\n"+list)
		if pending == 0 {
			_, _ = io.WriteString(a.env.Stdout, a.initEnding(c, nil, 0, false, st))
			return nil
		}
		_, _ = io.WriteString(a.env.Stdout, "\n")
		apply, err = a.asker().confirm("Make these changes?", "", true)
		if errors.Is(err, errAborted) || (err == nil && !apply) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
		// The changes are already on screen; say only how it ended.
		list = ""
	}
	if apply {
		if err := a.applyInit(ctx, setups, modeChange); err != nil {
			return err
		}
		if err := a.recordInit(setups, c.scope); err != nil {
			return err
		}
	}
	text := list + a.allowAdvice(c, setups, interactive) + a.initEnding(c, setups, pending, apply, st)
	if interactive && apply {
		text += nextSteps(setups, st)
	}
	a.emit(struct {
		Applied       bool           `json:"applied"`
		Scope         string         `json:"scope"`
		Delivery      *initDelivery  `json:"delivery"`
		AllowCommands bool           `json:"allow_commands"`
		Harnesses     []harnessSetup `json:"harnesses"`
	}{apply, c.scope, modeChange, c.allow, setups}, text)
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
		if _, ok := a.registry().Get(h); !ok {
			return usageError(fmt.Sprintf("%q is not a harness aboard init sets up; use %s.", h, harness.OrList(a.registry().Names())), use)
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
	setups := slices.Clone(known)
	for i := range setups {
		s := &setups[i]
		s.Changes = []fileChange{}
		h, ok := a.registry().Get(s.Name)
		if !ok || !s.Detected || (c.harnesses != nil && !slices.Contains(c.harnesses, s.Name)) {
			continue
		}
		specs := a.withHome(h.Hooks(exe, h.Version(ctx, a.henv())))
		hooksAt := -1
		for _, it := range h.Items(a.installEnv(), c.scope) {
			switch {
			case it.Kind == harness.ItemHooks:
				hk, err := hooksChange(it.Path, s.Name, specs)
				if err != nil {
					return nil, err
				}
				hooksAt = len(s.Changes)
				s.Changes = append(s.Changes, hk)
				if scopes, _, err := a.installedScopes(h, specs); err == nil {
					s.otherScope = slices.ContainsFunc(scopes, func(sc string) bool { return sc != c.scope })
				}
			case it.Kind == harness.ItemAllowRule && !c.allow, it.Kind == harness.ItemConsent:
			case it.Kind == harness.ItemAllowRule && it.Path == "":
				// The rule goes in the settings file that holds the hooks.
				if hooksAt < 0 {
					continue
				}
				if err := allowSettings(&s.Changes[hooksAt], it); err != nil {
					return nil, err
				}
			default:
				ch, err := ownedChange(it, changeKind(it.Kind))
				if err != nil {
					return nil, err
				}
				s.Changes = append(s.Changes, ch)
			}
		}
	}
	a.markEdited(setups)
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

func hooksChange(path, name string, specs []harness.Hook) (fileChange, error) {
	c := fileChange{Path: path, Kind: "hooks", Action: actionCreate, perm: 0o644}
	for _, s := range specs {
		c.commands = append(c.commands, s.Event+": "+s.Handler.Command)
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
	data, changed, err := mergeHooks(old, name, specs)
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
func mergeHooks(data []byte, name string, specs []harness.Hook) (out []byte, changed bool, err error) {
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
		if raw, ok := hooks.get(spec.Event); ok && string(bytes.TrimSpace(raw)) != "null" {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, false, fmt.Errorf("hooks.%s: %w", spec.Event, err)
			}
		}
		want, err := json.Marshal(spec.Handler)
		if err != nil {
			return nil, false, fmt.Errorf("encode hook: %w", err)
		}
		found, eventChanged := false, false
		for gi, g := range groups {
			group, err := parseJSONObject(g)
			if err != nil {
				return nil, false, fmt.Errorf("hooks.%s: %w", spec.Event, err)
			}
			var handlers []json.RawMessage
			if raw, ok := group.get("hooks"); ok {
				if err := json.Unmarshal(raw, &handlers); err != nil {
					return nil, false, fmt.Errorf("hooks.%s: %w", spec.Event, err)
				}
			}
			groupChanged, ours := false, false
			for hi, h := range handlers {
				var have map[string]any
				if json.Unmarshal(h, &have) != nil {
					continue
				}
				command, _ := have["command"].(string)
				if !isAboardHook(command, name, spec.Arg) {
					continue
				}
				found, ours = true, true
				var wantMap map[string]any
				_ = json.Unmarshal(want, &wantMap)
				if !reflect.DeepEqual(have, wantMap) {
					handlers[hi], groupChanged = want, true
				}
			}
			// A hook with a matcher, such as Bash, keeps it on the group that holds it.
			matcherChanged := false
			if ours && spec.Matcher != "" {
				var have string
				if raw, ok := group.get("matcher"); ok {
					_ = json.Unmarshal(raw, &have)
				}
				if have != spec.Matcher {
					raw, _ := json.Marshal(spec.Matcher)
					group.set("matcher", raw)
					matcherChanged = true
				}
			}
			if groupChanged || matcherChanged {
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
			g, err := json.Marshal(struct {
				Matcher string            `json:"matcher,omitempty"`
				Hooks   []json.RawMessage `json:"hooks"`
			}{spec.Matcher, []json.RawMessage{want}})
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
			hooks.set(spec.Event, raw)
			changed = true
		}
	}
	removed, err := removeStaleHooks(hooks, name, specs)
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
// compact names the hook events of a hooks file on one line instead of each command,
// for a person who already chose what to set up.
func initList(setups []harnessSetup, mode *initDelivery, c initChoices, home string, st styles, compact bool) (list string, pending int) {
	var b strings.Builder
	short := func(p string) string { return shortPath(p, home) }
	for _, s := range setups {
		if !s.Detected {
			fmt.Fprintf(&b, "%s: %s\n", s.Name, st.dim("not found on this machine"))
			continue
		}
		if len(s.Changes) == 0 {
			fmt.Fprintf(&b, "%s: %s\n", s.Name, st.dim("not chosen"))
			continue
		}
		b.WriteString(st.heading(s.Name+", "+scopeText(c.scope)+":") + "\n")
		if s.otherScope {
			other := scopeGlobal
			if c.scope == scopeGlobal {
				other = scopeProject
			}
			fmt.Fprintf(&b, "  %s\n", st.warn("(its hooks are also set up "+scopeText(other)+"; with both, it may run each hook twice)"))
		}
		for _, ch := range s.Changes {
			kind := ch.Kind
			if ch.Edited {
				kind += "; " + st.warn("edited since aboard "+ch.WrittenBy+" wrote it")
			}
			fmt.Fprintf(&b, "  %s %s (%s)\n", actionStyle(st, ch.Action, fmt.Sprintf("%-9s", ch.Action)), short(ch.Path), kind)
			if ch.Action == actionUnchanged {
				continue
			}
			pending++
			lines, events := []string{}, []string{}
			for _, cmd := range ch.commands {
				if event, _, ok := strings.Cut(cmd, ": "); compact && ok && event != "allow" {
					events = append(events, event)
					continue
				}
				lines = append(lines, cmd)
			}
			if len(events) > 0 {
				lines = append([]string{"aboard hook " + s.Name + " on " + strings.Join(events, ", ")}, lines...)
			}
			for _, line := range lines {
				fmt.Fprintf(&b, "            %s\n", st.dim(line))
			}
			if ch.note != "" {
				b.WriteString("            " + st.dim("("+ch.note+")") + "\n")
			}
		}
	}
	if mode != nil {
		fmt.Fprintf(&b, "delivery for agents without their own mode: %s (%s)\n", st.name(string(mode.Mode)), actionStyle(st, mode.Action, mode.Action))
		if mode.Action != actionUnchanged {
			pending++
		}
	}
	return b.String(), pending
}

// allowAdvice recommends --allow-commands when init sets up a harness that can't reach
// Aboard without its allow rule, and no rule it reads here allows aboard yet. In a
// terminal the question already said why.
func (a *app) allowAdvice(c initChoices, setups []harnessSetup, interactive bool) string {
	if c.allow || interactive {
		return ""
	}
	var advice string
	for _, s := range setups {
		h, ok := a.registry().Get(s.Name)
		if !ok || len(s.Changes) == 0 {
			continue
		}
		req := requiredAllow(h)
		if req == nil || a.allowed(h, scopeGlobal) || a.allowed(h, c.scope) {
			continue
		}
		advice += req.Advice + "\n"
	}
	return advice
}

// actionStyle colors text by the file action it stands for.
func actionStyle(st styles, action, text string) string {
	switch action {
	case actionCreate:
		return st.ok(text)
	case actionUpdate, actionEdit:
		return st.warn(text)
	case actionDelete:
		return st.bad(text)
	}
	return st.dim(text)
}

// initEnding says what happened, or what to run to make the changes. Only the harnesses
// whose hook files were added or updated ask the person to trust the hooks again.
func (a *app) initEnding(c initChoices, setups []harnessSetup, pending int, applied bool, st styles) string {
	switch {
	case pending == 0:
		return st.ok("Nothing to change.") + "\n"
	case !applied:
		return "Run " + st.code(initCommand(c)) + " to make these changes.\n"
	}
	var trust []harness.Item
	var titles []string
	for _, s := range setups {
		h, ok := a.registry().Get(s.Name)
		if !ok {
			continue
		}
		consent, asks := harness.Find(h.Items(a.installEnv(), c.scope), harness.ItemConsent)
		changed := slices.ContainsFunc(s.Changes, func(ch fileChange) bool { return ch.Kind == "hooks" && ch.Action != actionUnchanged })
		if asks && changed {
			trust, titles = append(trust, consent), append(titles, h.Profile().Name)
		}
	}
	switch {
	case len(trust) == 0:
		return st.ok("Done.") + "\n"
	case len(trust) == 1:
		return st.ok("Done.") + " " + titles[0] + " asks you to trust new hooks before they run: " + trust[0].Text + ".\n" + projectEnding(c, trust)
	case !slices.ContainsFunc(trust, func(it harness.Item) bool { return it.Text != trust[0].Text }):
		return st.ok("Done.") + " " + harness.AndList(titles) +
			" ask you to trust new hooks before they run: " + trust[0].Text + " in each.\n" + projectEnding(c, trust)
	}
	text := st.ok("Done.")
	for i, t := range trust {
		text += " " + titles[i] + " asks you to trust new hooks before they run: " + t.Text + "."
	}
	return text + "\n" + projectEnding(c, trust)
}

// projectEnding is what a project setup that changed hook files adds to init's ending.
func projectEnding(c initChoices, trust []harness.Item) string {
	if c.scope != scopeProject {
		return ""
	}
	var end string
	for _, t := range trust {
		if t.ProjectText != "" {
			end += t.ProjectText + "\n"
		}
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
func removeStaleHooks(hooks *jsonObject, name string, specs []harness.Hook) (bool, error) {
	wanted := map[string]bool{}
	for _, s := range specs {
		wanted[s.Event+" "+s.Arg] = true
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
					if arg, ok := aboardHookArg(have.Command, name); ok && !wanted[event+" "+arg] {
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
