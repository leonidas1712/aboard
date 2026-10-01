package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

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

// codexContextLimit lets a Codex hook add a whole bundle: 32 KiB is about 8,192 tokens.
const codexContextLimit = 8192

// stopHookTimeout is the stop hook's timeout in seconds. The hook waits for as long as
// the session is idle.
const stopHookTimeout = 86400

func claudeHooks(exe string) []hookSpec {
	cmd := func(arg string) string { return shellWord(exe) + " hook claude-code " + arg }
	return []hookSpec{
		{"SessionStart", "session-start", hookHandler{Type: "command", Command: cmd("session-start"), Timeout: 30}},
		{"UserPromptSubmit", "prompt", hookHandler{Type: "command", Command: cmd("prompt"), Timeout: 10}},
		{"Stop", "stop", hookHandler{Type: "command", Command: cmd("stop"), Timeout: stopHookTimeout, AsyncRewake: true}},
		{"PostToolUse", "tool", hookHandler{Type: "command", Command: cmd("tool"), Timeout: 10}},
		{"SessionEnd", "end", hookHandler{Type: "command", Command: cmd("end"), Timeout: 10}},
	}
}

func codexHooks(exe string) []hookSpec {
	cmd := func(arg string) string { return shellWord(exe) + " hook codex " + arg }
	return []hookSpec{
		{"SessionStart", "session-start", hookHandler{Type: "command", Command: cmd("session-start"), Timeout: 30}},
		{"PostToolUse", "tool", hookHandler{Type: "command", Command: cmd("tool"), Timeout: 30, AdditionalContextLimit: codexContextLimit}},
		// Codex caps SessionEnd hooks at 3 seconds; closing a session is one socket call.
		{"SessionEnd", "end", hookHandler{Type: "command", Command: cmd("end"), Timeout: 3}},
	}
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

// harnessSetup is what aboard init does for one harness.
type harnessSetup struct {
	Name     string       `json:"name"`
	Detected bool         `json:"detected"`
	Changes  []fileChange `json:"changes"`
}

// fileChange is one file aboard init writes.
type fileChange struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	data   []byte
	perm   os.FileMode
	// commands are the hook commands the file runs, shown in text output.
	commands []string
}

// File change actions.
const (
	actionCreate    = "create"
	actionUpdate    = "update"
	actionUnchanged = "unchanged"
)

// runInit adds the Aboard skill and the delivery hooks to Claude Code and Codex. Without
// --yes it only lists the changes.
func runInit(_ context.Context, a *app, args []string) error {
	const use = "aboard init [--yes] [--json]"
	flags := a.flags("init")
	yes := flags.Bool("yes", false, "make the changes instead of listing them")
	if _, err := a.parse(flags, args, use, 0, 0); err != nil {
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
	codexHome := a.env.Getenv("CODEX_HOME")
	if !filepath.IsAbs(codexHome) {
		codexHome = filepath.Join(home, ".codex")
	}
	claudeDir := filepath.Join(home, ".claude")
	setups := []harnessSetup{
		{Name: "claude-code", Detected: detected(claudeDir, "claude")},
		{Name: "codex", Detected: detected(codexHome, "codex")},
	}
	plans := []struct {
		skill string
		hooks string
		specs []hookSpec
	}{
		{filepath.Join(claudeDir, "skills", "aboard", "SKILL.md"), filepath.Join(claudeDir, "settings.json"), claudeHooks(exe)},
		{filepath.Join(home, ".agents", "skills", "aboard", "SKILL.md"), filepath.Join(codexHome, "hooks.json"), codexHooks(exe)},
	}
	for i := range setups {
		setups[i].Changes = []fileChange{}
		if !setups[i].Detected {
			continue
		}
		sk, err := skillChange(plans[i].skill)
		if err != nil {
			return err
		}
		hk, err := hooksChange(plans[i].hooks, setups[i].Name, plans[i].specs)
		if err != nil {
			return err
		}
		setups[i].Changes = append(setups[i].Changes, sk, hk)
	}
	if *yes {
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
	}
	a.emit(struct {
		Applied   bool           `json:"applied"`
		Harnesses []harnessSetup `json:"harnesses"`
	}{*yes, setups}, initText(setups, *yes, home))
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

func initText(setups []harnessSetup, applied bool, home string) string {
	var b strings.Builder
	short := func(p string) string {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
		return p
	}
	pending := 0
	for _, s := range setups {
		if !s.Detected {
			fmt.Fprintf(&b, "%s: not found on this machine\n", s.Name)
			continue
		}
		fmt.Fprintf(&b, "%s:\n", s.Name)
		for _, c := range s.Changes {
			fmt.Fprintf(&b, "  %-9s %s (%s)\n", c.Action, short(c.Path), c.Kind)
			if c.Action == actionUnchanged {
				continue
			}
			pending++
			for _, cmd := range c.commands {
				fmt.Fprintf(&b, "            %s\n", cmd)
			}
		}
	}
	switch {
	case pending == 0:
		b.WriteString("Nothing to change.\n")
	case !applied:
		b.WriteString("Run aboard init --yes to make these changes.\n")
	default:
		b.WriteString("Done. Claude Code and Codex ask you to trust new hooks before they run: review them in /hooks in each.\n")
	}
	return b.String()
}
