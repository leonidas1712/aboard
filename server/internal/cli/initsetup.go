package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
	"github.com/leonidas1712/aboard/server/internal/harness/registry"
)

// Where aboard init installs: under the home directory for every project, or under the
// working directory for that project only.
const (
	scopeGlobal  = harness.ScopeGlobal
	scopeProject = harness.ScopeProject
)

// registry returns the harnesses Aboard knows, once per command.
func (a *app) registry() harness.Set {
	if a.harnesses == nil {
		a.harnesses = registry.Harnesses()
	}
	return a.harnesses
}

// henv is what a harness reads from this command's environment.
func (a *app) henv() harness.Env {
	return harness.Env{Getenv: a.env.Getenv, Dir: a.env.Dir}
}

// item returns where a harness's item of a kind goes in a scope.
func (a *app) item(h harness.Harness, scope string, kind harness.ItemKind) (harness.Item, bool) {
	return harness.Find(h.Items(a.henv(), scope), kind)
}

// hooksFile returns the file that holds a harness's hooks in a scope, or "".
func (a *app) hooksFile(h harness.Harness, scope string) string {
	it, _ := a.item(h, scope, harness.ItemHooks)
	return it.Path
}

// installedScopes returns the scopes in which a harness's Aboard hooks are installed,
// with the hook file of each: those where the session-start hook, which every other hook
// relies on, is there. Whether the rest match this aboard is checkHooksCurrent's job.
func (a *app) installedScopes(h harness.Harness, specs []harness.Hook) (scopes, files []string, err error) {
	start := sessionStartHooks(h, specs)
	for _, scope := range a.scopes() {
		path := a.hooksFile(h, scope)
		if path == "" {
			continue
		}
		missing, err := hooksMissing(path, h.Profile().Harness, start)
		if err != nil {
			return nil, nil, err
		}
		if len(missing) == 0 {
			scopes, files = append(scopes, scope), append(files, path)
		}
	}
	return scopes, files, nil
}

// sessionStartHooks returns the hooks that register a session.
func sessionStartHooks(h harness.Harness, specs []harness.Hook) []harness.Hook {
	return slices.DeleteFunc(slices.Clone(specs), func(s harness.Hook) bool {
		c, ok := h.HookCall(s.Arg, harness.HookInput{})
		return !ok || c.Op != harness.OpSessionStart
	})
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

// addAllowRule adds an allow rule to permissions.allow in a JSON settings file that
// holds a harness's hooks, keeping everything else. changed is false when the rule, or
// a form of it the harness also accepts, is already there.
func addAllowRule(data []byte, rule harness.Item) (out []byte, changed bool, err error) {
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
	if slices.Contains(allow, rule.Rule) || slices.ContainsFunc(rule.Accepts, func(r string) bool { return slices.Contains(allow, r) }) {
		return data, false, nil
	}
	raw, err := json.Marshal(append(allow, rule.Rule))
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

// allowSettings adds an allow rule to the planned change of the settings file that
// holds the hooks.
func allowSettings(c *fileChange, rule harness.Item) error {
	data, changed, err := addAllowRule(c.data, rule)
	if err != nil {
		return &Error{
			Code: "invalid_request", Message: "Couldn't read the permissions in " + c.Path + ": " + err.Error(),
			Hint: "Fix the file so permissions.allow is a list of strings, then run aboard init again.", Err: err,
		}
	}
	c.Allow = []string{rule.Rule}
	if changed {
		c.data = data
		c.allowAdded = true
		if c.Action == actionUnchanged {
			c.Action = actionUpdate
		}
		c.commands = append(c.commands, "allow: "+rule.Rule)
	}
	return nil
}

// ownedChange plans a file Aboard owns: the skill, a file inside the harness, or the
// file that holds only an allow rule.
func ownedChange(it harness.Item, kind string) (fileChange, error) {
	c := fileChange{Path: it.Path, Kind: kind, Action: actionCreate, data: it.Data, perm: 0o644}
	if it.Kind == harness.ItemAllowRule {
		c.Allow, c.commands, c.note = []string{it.Rule}, []string{it.Rule}, it.Note
	}
	old, err := os.ReadFile(filepath.Clean(it.Path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", it.Path, err)
	case bytes.Equal(old, it.Data):
		c.Action = actionUnchanged
	default:
		c.Action = actionUpdate
	}
	return c, nil
}

// changeKind is the kind an install item has in init's and uninstall's output and in
// the install manifest.
func changeKind(kind harness.ItemKind) string {
	if kind == harness.ItemAllowRule {
		return "permissions"
	}
	return string(kind)
}

// allowedIn returns the file that holds a harness's allow rule for aboard in a scope,
// if any: the settings file that holds the hooks, or, for a rule in a file of its own,
// any file with the same extension in that folder, since the harness reads them all.
// Spaces in the rule don't matter there.
func (a *app) allowedIn(h harness.Harness, scope string) (string, bool) {
	rule, ok := a.item(h, scope, harness.ItemAllowRule)
	if !ok {
		return "", false
	}
	if rule.Path == "" {
		path := a.hooksFile(h, scope)
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", false
		}
		_, changed, err := addAllowRule(data, rule)
		return path, err == nil && !changed
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(rule.Path), "*"+filepath.Ext(rule.Path)))
	want := strings.Join(strings.Fields(rule.Rule), "")
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Join(strings.Fields(line), "") == want {
				return f, true
			}
		}
	}
	return "", false
}

// allowed reports whether a harness's files in a scope hold the allow rule for aboard.
func (a *app) allowed(h harness.Harness, scope string) bool {
	_, ok := a.allowedIn(h, scope)
	return ok
}

// allowedAnywhere reports whether a rule the harness reads from here allows aboard.
func (a *app) allowedAnywhere(h harness.Harness) bool {
	return slices.ContainsFunc(a.scopes(), func(sc string) bool { return a.allowed(h, sc) })
}

// requiredAllow returns what Aboard says about a harness's allow rule when the harness
// can't reach Aboard without it, or nil.
func requiredAllow(h harness.Harness) *harness.AllowRequired {
	for _, s := range h.Profile().Install {
		if s.Kind == harness.ItemAllowRule && s.Required != nil {
			return s.Required
		}
	}
	return nil
}

// initChoices are the answers aboard init works from, from flags or questions.
type initChoices struct {
	scope     string
	harnesses []string // nil means every detected harness
	delivery  delivery.Mode
	allow     bool
}

// chooses reports whether c sets up a harness, given the harnesses found.
func (c initChoices) chooses(name string, found []string) bool {
	if c.harnesses == nil {
		return slices.Contains(found, name)
	}
	return slices.Contains(c.harnesses, name)
}
