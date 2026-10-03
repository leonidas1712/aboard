// Package harness is what Aboard knows about each harness it sets up and delivers to,
// behind one interface. A harness's profile (adapters/<harness>/profile.yaml) holds the
// data: names, variables, files, hooks, checks. Generic reads the profile for the jobs
// every harness shares, and a harness's own package (claudecode, codex) overrides only
// what the profile can't say. The CLI and the delivery daemon reach harnesses only
// through this package and the registry, so they name none.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Harness is one harness Aboard sets up and delivers to.
type Harness interface {
	// Profile is the harness's profile.
	Profile() *Profile
	// Detected reports whether the harness looks installed on this machine.
	Detected(e Env) bool
	// ConfigDir is the folder the harness reads its global config from.
	ConfigDir(e Env) string
	// Version is what the harness reports as its version, or "" when no hook depends
	// on it or it can't be told.
	Version(ctx context.Context, e Env) string
	// Hooks are the hook entries aboard init installs for a harness of this version
	// ("" means the newest), each running exe.
	Hooks(exe, version string) []Hook
	// Items are what aboard init installs in a scope, in order, with their paths.
	Items(e Env, scope string) []Item
	// InstalledChecks are aboard doctor's checks that the harness is there and can be
	// delivered to. installed is false when doctor checks nothing more of it.
	InstalledChecks(ctx context.Context, e Env) (checks []CheckResult, installed bool)
	// HookCall says what "aboard hook <harness> <event>" does with the hook's input.
	HookCall(event string, in HookInput) (Call, bool)
	// Fix says how to fix a delivery that stopped for a reason this harness's
	// adapter gave, or "".
	Fix(reason string) string
	// Adapter is the delivery daemon's adapter for the harness, or nil when it has no
	// automatic delivery.
	Adapter(clientVersion string) delivery.Adapter
}

// Scopes aboard init installs in: under the home directory for every project, or under
// the working directory for that project only.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Env is what a harness reads from the machine.
type Env struct {
	// Getenv reads an environment variable.
	Getenv func(string) string
	// Dir is the working directory, the project of a project setup.
	Dir string
}

// Home is the home directory.
func (e Env) Home() string { return e.Getenv("HOME") }

// LookPath finds a program on the PATH, or returns "".
func (e Env) LookPath(name string) string {
	for _, dir := range filepath.SplitList(e.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// ItemKind is the kind of one thing aboard init installs.
type ItemKind string

// Install item kinds.
const (
	// ItemSkill is the Aboard skill, a file Aboard owns.
	ItemSkill ItemKind = "skill"
	// ItemHooks are the delivery hooks, merged into a file shared with the person.
	ItemHooks ItemKind = "hooks"
	// ItemFile is a file inside the harness that Aboard owns, such as an extension.
	ItemFile ItemKind = "file"
	// ItemAllowRule lets the agent run aboard commands without a permission prompt.
	ItemAllowRule ItemKind = "allow-rule"
	// ItemConsent is a step the person takes, such as trusting new hooks.
	ItemConsent ItemKind = "consent"
)

// Item is one thing aboard init installs in a scope.
type Item struct {
	InstallSpec
	// Path is where it goes in this scope. An allow rule without a path goes in the
	// hooks file; a consent step has none.
	Path string
	// Data is what Aboard writes to a file it owns: the skill, a file, or the file
	// holding an allow rule.
	Data []byte
}

// Find returns the first item of a kind.
func Find(items []Item, kind ItemKind) (Item, bool) {
	for _, it := range items {
		if it.Kind == kind {
			return it, true
		}
	}
	return Item{}, false
}

// RulesFile is the content of a file Aboard owns that holds only an allow rule.
func RulesFile(rule string) []byte {
	return []byte("# Added by aboard init: run aboard commands without asking.\n" + rule + "\n")
}

// Hook is one hook entry aboard init installs.
type Hook struct {
	// Event is the harness's name for the hook event.
	Event string
	// Arg is the event argument of "aboard hook <harness> <arg>".
	Arg string
	// Matcher is the matcher of the hook's group, such as Bash, or "" for none.
	Matcher string
	Handler Handler
}

// Handler is one hook command in a harness's hook settings.
type Handler struct {
	Type    string
	Command string
	// Timeout is in seconds; 0 leaves it out.
	Timeout int
	// Options are more settings in the harness's own words, written after the timeout.
	Options map[string]any
}

// MarshalJSON writes type, command and timeout, then the options with their keys
// sorted, so an entry is written the same way every time. A harness asks the person
// to trust its hooks again when an entry's text changes.
func (h Handler) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	field := func(key string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(raw)
		return nil
	}
	b.WriteByte('{')
	if err := field("type", h.Type); err != nil {
		return nil, err
	}
	if err := field("command", h.Command); err != nil {
		return nil, err
	}
	if h.Timeout != 0 {
		if err := field("timeout", h.Timeout); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(h.Options))
	for k := range h.Options {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if err := field(k, h.Options[k]); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Op is what a hook does, in terms common to every harness.
type Op string

// Hook operations.
const (
	// OpSessionStart registers the session.
	OpSessionStart Op = "session-start"
	// OpPrompt marks a turn running.
	OpPrompt Op = "prompt"
	// OpWait waits while the session is idle, and wakes it with a bundle.
	OpWait Op = "wait"
	// OpTurnEnd marks the turn ended.
	OpTurnEnd Op = "turn-end"
	// OpTool is a tool boundary, where the owner's messages are added to the turn.
	OpTool Op = "tool"
	// OpEnd closes the session.
	OpEnd Op = "end"
	// OpMarkSubagent marks an aboard command a subagent is about to run as the
	// subagent's, so it can't act as the parent.
	OpMarkSubagent Op = "mark-subagent"
)

// SubagentEnv is the variable that marks a command as run by a subagent of the session
// it runs in. Its value is the harness's id for the subagent.
const SubagentEnv = "ABOARD_SUBAGENT"

// HookInput is the part of a harness's hook input the hooks read.
type HookInput struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
	// AgentID is set when a hook fires inside a subagent.
	AgentID string `json:"agent_id"`
	// HookEventName is the event the harness ran the hook for; a tool hook's output
	// names it back.
	HookEventName string `json:"hook_event_name"`
	// Prompt is the prompt text, on a prompt hook.
	Prompt string `json:"prompt"`
	// ToolInput is the input of the tool about to run, on a pre-tool hook. For a shell
	// command it holds the command and the tool's other settings.
	ToolInput json.RawMessage `json:"tool_input"`
}

// Call is what one hook does.
type Call struct {
	Op Op
	// Wake is true for a prompt that is the harness handing back the bundle a waiting
	// hook woke the session with: that prompt is the wake itself, not a later event.
	Wake bool
}

// CheckResult is one line of aboard doctor.
type CheckResult struct {
	Name, Level, Code, Message, Fix string
}

// Check levels.
const (
	LevelOK      = "ok"
	LevelWarning = "warning"
	LevelError   = "error"
)

// ShellWord quotes s for a shell command line when it needs quoting.
func ShellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// VersionAtLeast reports whether the first dotted version number in text, such as
// "2.1.288 (Claude Code)", is at least minimum. Text without one counts as new enough.
func VersionAtLeast(text, minimum string) bool {
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
