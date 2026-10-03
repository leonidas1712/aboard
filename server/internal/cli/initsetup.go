package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Where aboard init installs: under the home directory for every project, or under the
// working directory for that project only.
const (
	scopeGlobal  = "global"
	scopeProject = "project"
)

// The allow rules that let an agent run aboard commands without a permission prompt,
// each in its harness's own format.
const (
	claudeAllowRule = "Bash(aboard *)"
	codexAllowRule  = `prefix_rule(pattern=["aboard"], decision="allow")`
)

// codexRules is the Codex rules file aboard init writes for the allow rule.
var codexRules = []byte("# Added by aboard init: run aboard commands without asking.\n" + codexAllowRule + "\n")

// harnessFiles are the files aboard init writes for one harness in one scope. Claude Code
// keeps its allow rule in the settings file that holds its hooks.
type harnessFiles struct {
	skill, hooks, allow string
}

// configDirs are where each harness keeps its global config: the variable that names the
// folder, and the folder under the home directory when the variable isn't set. Each
// harness profile's config_dir says the same.
var configDirs = map[string]struct{ env, home string }{
	"claude-code": {"CLAUDE_CONFIG_DIR", ".claude"},
	"codex":       {"CODEX_HOME", ".codex"},
}

// configDir returns the folder a harness reads its global config from: the folder its
// variable names when that is an absolute path, else the default under HOME.
func (a *app) configDir(harness string) string {
	d := configDirs[harness]
	if dir := a.env.Getenv(d.env); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(a.env.Getenv("HOME"), d.home)
}

// setupFiles returns where aboard init writes for a harness in a scope. Global setup goes
// in the harness's config folder, except Codex's skill, which Codex reads from
// ~/.agents/skills wherever CODEX_HOME points. In a project,
// Claude Code's hooks go in settings.local.json, the file meant for one person's machine,
// since they name this machine's aboard binary.
func (a *app) setupFiles(harness, scope string) harnessFiles {
	home, dir := a.env.Getenv("HOME"), a.env.Dir
	skill := filepath.Join("skills", "aboard", "SKILL.md")
	switch {
	case harness == "claude-code" && scope == scopeProject:
		settings := filepath.Join(dir, ".claude", "settings.local.json")
		return harnessFiles{filepath.Join(dir, ".claude", skill), settings, settings}
	case harness == "claude-code":
		settings := filepath.Join(a.configDir(harness), "settings.json")
		return harnessFiles{filepath.Join(a.configDir(harness), skill), settings, settings}
	case scope == scopeProject:
		return harnessFiles{
			filepath.Join(dir, ".agents", skill), filepath.Join(dir, ".codex", "hooks.json"),
			filepath.Join(dir, ".codex", "rules", "aboard.rules"),
		}
	default:
		return harnessFiles{
			filepath.Join(home, ".agents", skill), filepath.Join(a.configDir(harness), "hooks.json"),
			filepath.Join(a.configDir(harness), "rules", "aboard.rules"),
		}
	}
}

// installedScopes returns the scopes in which a harness's Aboard hooks are installed,
// with the hook file of each: those where the session-start hook, which every other hook
// relies on, is there. Whether the rest match this aboard is checkHooksCurrent's job.
func (a *app) installedScopes(harness string, specs []hookSpec) (scopes, files []string, err error) {
	start := slices.DeleteFunc(slices.Clone(specs), func(s hookSpec) bool { return s.arg != "session-start" })
	for _, scope := range a.scopes() {
		path := a.setupFiles(harness, scope).hooks
		missing, err := hooksMissing(path, harness, start)
		if err != nil {
			return nil, nil, err
		}
		if len(missing) == 0 {
			scopes, files = append(scopes, scope), append(files, path)
		}
	}
	return scopes, files, nil
}

// scopes returns the scopes aboard can install in from the working directory: both,
// unless the working directory is the home directory, where they are the same place.
func (a *app) scopes() []string {
	if a.env.Dir == a.env.Getenv("HOME") {
		return []string{scopeGlobal}
	}
	return []string{scopeGlobal, scopeProject}
}

// scopeText names a scope for text output.
func scopeText(scope string) string {
	if scope == scopeProject {
		return "in this project"
	}
	return "everywhere"
}

// addClaudeAllow adds the allow rule to the permissions in Claude Code settings, keeping
// everything else. changed is false when a rule for aboard is already there.
func addClaudeAllow(data []byte) (out []byte, changed bool, err error) {
	root, err := parseJSONObject(data)
	if err != nil {
		return nil, false, err
	}
	perms := newJSONObject()
	if raw, ok := root.get("permissions"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if perms, err = parseJSONObject(raw); err != nil {
			return nil, false, fmt.Errorf("permissions: %w", err)
		}
	}
	var allow []string
	if raw, ok := perms.get("allow"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &allow); err != nil {
			return nil, false, fmt.Errorf("permissions.allow: %w", err)
		}
	}
	// Claude Code also accepts the older prefix form.
	if slices.Contains(allow, claudeAllowRule) || slices.Contains(allow, "Bash(aboard:*)") {
		return data, false, nil
	}
	raw, err := json.Marshal(append(allow, claudeAllowRule))
	if err != nil {
		return nil, false, fmt.Errorf("encode permissions: %w", err)
	}
	perms.set("allow", raw)
	if raw, err = json.Marshal(perms); err != nil {
		return nil, false, fmt.Errorf("encode permissions: %w", err)
	}
	root.set("permissions", raw)
	out, err = encodeIndented(root)
	return out, true, err
}

// allowSettings adds Claude Code's allow rule to the planned change of its settings file.
func allowSettings(c *fileChange) error {
	data, changed, err := addClaudeAllow(c.data)
	if err != nil {
		return &Error{
			Code: "invalid_request", Message: "Couldn't read the permissions in " + c.Path + ": " + err.Error(),
			Hint: "Fix the file so permissions.allow is a list of strings, then run aboard init again.", Err: err,
		}
	}
	c.Allow = []string{claudeAllowRule}
	if changed {
		c.data = data
		if c.Action == actionUnchanged {
			c.Action = actionUpdate
		}
		c.commands = append(c.commands, "allow: "+claudeAllowRule)
	}
	return nil
}

// rulesChange plans the Codex rules file that holds the allow rule. Aboard owns the file.
func rulesChange(path string) (fileChange, error) {
	c := fileChange{
		Path: path, Kind: "permissions", Action: actionCreate, Allow: []string{codexAllowRule},
		data: codexRules, perm: 0o644, commands: []string{codexAllowRule},
	}
	old, err := os.ReadFile(filepath.Clean(path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", path, err)
	case bytes.Equal(old, codexRules):
		c.Action = actionUnchanged
	default:
		c.Action = actionUpdate
	}
	return c, nil
}

// initChoices are the answers aboard init works from, from flags or questions.
type initChoices struct {
	scope     string
	harnesses []string // nil means every detected harness
	delivery  delivery.Mode
	allow     bool
}

// prompter asks questions on a terminal.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// ask prints a question and returns the trimmed answer, or def for an empty answer or
// the end of input.
func (p *prompter) ask(question, def string) string {
	_, _ = fmt.Fprint(p.out, question)
	line, _ := p.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

// choose asks until the answer is one of options.
func (p *prompter) choose(question, def string, options []string) string {
	for {
		answer := strings.ToLower(p.ask(question, def))
		if slices.Contains(options, answer) {
			return answer
		}
		_, _ = fmt.Fprintf(p.out, "Answer %s.\n", strings.Join(options, ", "))
	}
}

// yes asks a yes-or-no question; anything but yes is no.
func (p *prompter) yes(question string) bool {
	switch strings.ToLower(p.ask(question+" [y/N] ", "n")) {
	case "y", "yes":
		return true
	}
	return false
}

// yesByDefault asks a yes-or-no question whose empty answer is yes; anything but no is
// yes.
func (p *prompter) yesByDefault(question string) bool {
	switch strings.ToLower(p.ask(question+" [Y/n] ", "y")) {
	case "n", "no":
		return false
	}
	return true
}

// codexAllowWhy says in one line why Codex needs aboard commands allowed.
const codexAllowWhy = "Codex's sandbox blocks network access; aboard needs to reach its local server."

// codexAllowAdvice is the line init adds when it sets up Codex without the allow rule.
const codexAllowAdvice = "Codex's sandbox blocks network access, so aboard commands Codex runs can't reach the local server; " +
	"add --allow-commands to let Codex run them outside its sandbox.\n"

// choosesCodex reports whether c sets up Codex, given the harnesses found.
func choosesCodex(c initChoices, found []string) bool {
	if c.harnesses == nil {
		return slices.Contains(found, "codex")
	}
	return slices.Contains(c.harnesses, "codex")
}

// askInit asks the questions no flag answered. set holds the flags that were given.
func (a *app) askInit(p *prompter, c *initChoices, found []string, set map[string]bool, current delivery.Mode) {
	if !set["harness"] && len(found) > 1 {
		answer := p.ask(fmt.Sprintf("Set up which harnesses? %s [all] ", strings.Join(found, ", ")), "all")
		if answer != "all" {
			var l listFlag
			_ = l.Set(answer)
			c.harnesses = l
		}
	}
	if !set["scope"] {
		answer := p.choose(fmt.Sprintf("Install everywhere, or only in this project (%s)? everywhere/project [everywhere] ", a.env.Dir),
			"everywhere", []string{"everywhere", "global", "project"})
		c.scope = map[string]string{"everywhere": scopeGlobal, "global": scopeGlobal, "project": scopeProject}[answer]
	}
	if !set["delivery"] {
		_, _ = fmt.Fprintln(p.out, "Delivery wakes a session when a message arrives: auto for every message, humans only for messages from people, off never.")
		c.delivery = delivery.Mode(p.choose(fmt.Sprintf("Delivery for agents on this machine? auto/humans/off [%s] ", current),
			string(current), []string{"auto", "humans", "off"}))
	}
	if set["allow-commands"] {
		return
	}
	const question = "Let agents run aboard commands without a permission prompt?"
	if choosesCodex(*c, found) {
		// Without the rule, Codex runs aboard in its sandbox, where it can't reach the server.
		_, _ = fmt.Fprintln(p.out, codexAllowWhy)
		c.allow = p.yesByDefault(question)
		return
	}
	c.allow = p.yes(question)
}
