package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/adapters"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/extension"
	"github.com/leonidas1712/aboard/server/internal/delivery/idlehook"
	skill "github.com/leonidas1712/aboard/skills/aboard"
)

// Generic is a harness made to work from its profile alone. A harness's own package
// embeds it and overrides only the methods where the harness really differs.
type Generic struct {
	profile *Profile
	// version is what the harness reported, once asked.
	version *string
}

var _ Harness = (*Generic)(nil)

// NewGeneric returns the generic implementation of the harness a profile describes.
func NewGeneric(p *Profile) *Generic { return &Generic{profile: p} }

// MustLoad returns the generic implementation of a harness whose profile is built into
// aboard. The profiles are part of the binary and checked against their schema by the
// tests, so one that can't be read is a broken build.
func MustLoad(name string) *Generic {
	p, err := LoadProfile(name)
	if err != nil {
		panic(err)
	}
	return NewGeneric(p)
}

// Profile returns the harness's profile.
func (g *Generic) Profile() *Profile { return g.profile }

// Detected reports whether the harness looks installed: its config folder exists or
// its command is on the PATH.
func (g *Generic) Detected(e Env) bool {
	if dir := g.ConfigDir(e); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	_, err := exec.LookPath(g.profile.Command)
	return err == nil
}

// ConfigDir returns the folder the harness reads its global config from: the folder
// its variable names when that is an absolute path, else the default under HOME.
func (g *Generic) ConfigDir(e Env) string {
	d := g.profile.ConfigDir
	if d.Env == "" && d.Default == "" {
		return ""
	}
	if dir := e.Getenv(d.Env); d.Env != "" && filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(e.Home(), d.Default)
}

// Version runs the harness's installed check, which prints its version, when one of
// its hooks depends on the version. It is asked once.
func (g *Generic) Version(ctx context.Context, e Env) string {
	if g.version != nil {
		return *g.version
	}
	v := ""
	run := g.profile.Checks.Installed.Run
	if g.versionMatters() && len(run) > 0 {
		if path := e.LookPath(run[0]); path != "" {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			cmd := exec.CommandContext(cctx, path, run[1:]...) //nolint:gosec // the harness's own command, from its profile
			cmd.Env = append(os.Environ(), "HOME="+e.Home(), "PATH="+e.Getenv("PATH"))
			if out, err := cmd.Output(); err == nil {
				v = string(out)
			}
			cancel()
		}
	}
	g.version = &v
	return v
}

// versionMatters reports whether a hook is installed differently on older versions.
func (g *Generic) versionMatters() bool {
	for _, h := range g.profile.Delivery.Hooks {
		if h.Since != "" {
			return true
		}
	}
	return false
}

// Hooks returns the profile's hooks, each running "exe hook <harness> <run>". A hook
// whose event first appeared after this version is installed on its fallback events.
func (g *Generic) Hooks(exe, version string) []Hook {
	var hooks []Hook
	for _, s := range g.profile.Delivery.Hooks {
		h := Handler{Type: "command", Command: ShellWord(exe) + " hook " + g.profile.Harness + " " + s.Run, Timeout: s.Timeout, Options: s.Options}
		if s.Since != "" && !VersionAtLeast(version, s.Since) {
			for _, event := range s.Fallback {
				hooks = append(hooks, Hook{Event: event, Arg: s.Run, Matcher: s.Matcher, Handler: h})
			}
			continue
		}
		hooks = append(hooks, Hook{Event: s.Event, Arg: s.Run, Matcher: s.Matcher, Handler: h})
	}
	return hooks
}

// Items returns the profile's install items with their paths in a scope.
func (g *Generic) Items(e Env, scope string) []Item {
	items := make([]Item, 0, len(g.profile.Install))
	for _, s := range g.profile.Install {
		it := Item{InstallSpec: s}
		where := s.Global
		if scope == ScopeProject {
			where = s.Project
		}
		if where != "" {
			it.Path = g.resolve(e, scope, where)
		}
		switch {
		case s.Kind == ItemSkill:
			it.Data = skill.Skill
		case s.Kind == ItemAllowRule && it.Path != "":
			it.Data = RulesFile(s.Rule)
		case s.Kind == ItemFile:
			it.Data = FillFile(s.Source, e)
		}
		items = append(items, it)
	}
	return items
}

// resolve turns a profile path into a path on this machine: under the project for a
// project setup, else under the config folder or the home directory.
func (g *Generic) resolve(e Env, scope, path string) string {
	path = filepath.FromSlash(path)
	if scope == ScopeProject {
		return filepath.Join(e.Dir, path)
	}
	if rest, ok := strings.CutPrefix(path, "{config_dir}"+string(filepath.Separator)); ok {
		return filepath.Join(g.ConfigDir(e), rest)
	}
	return filepath.Join(e.Home(), path)
}

// InstalledChecks reports a harness that isn't installed. An installed one needs no
// line of its own: its hooks' check follows.
func (g *Generic) InstalledChecks(_ context.Context, e Env) ([]CheckResult, bool) {
	if g.Detected(e) {
		return nil, true
	}
	p := g.profile
	return []CheckResult{{
		Name: p.CheckName + "_hooks", Level: LevelWarning, Code: strings.ReplaceAll(p.Harness, "-", "_") + "_not_installed",
		Message: p.Harness + ": not installed", Fix: "install " + p.Name + ", or ignore this if you don't use it",
	}}, false
}

// HookCall finds the hook the profile lists for an event argument.
func (g *Generic) HookCall(event string, _ HookInput) (Call, bool) {
	for _, s := range g.profile.Delivery.Hooks {
		if s.Run != event {
			continue
		}
		op := s.Op
		if op == "" {
			op = Op(s.Run)
		}
		return Call{Op: op}, true
	}
	return Call{}, false
}

// Fix has nothing to add: the generic adapters give no reasons of their own.
func (g *Generic) Fix(string) string { return "" }

// Adapter returns the shared adapter for the harness's delivery mechanism, or nil.
func (g *Generic) Adapter(string) delivery.Adapter {
	switch {
	case g.profile.HasCapability("idle-hook"):
		return idlehook.Adapter{Name: g.profile.Harness}
	case g.profile.HasCapability("extension"):
		return extension.Adapter{Name: g.profile.Harness}
	}
	return nil
}

// FillFile returns a file built into aboard for installing inside a harness, with this
// machine's aboard binary and ABOARD_HOME written where the file says {aboard_binary}
// and {aboard_home}. Each is written as the inside of a JSON string, which reads the same
// in a JavaScript or TypeScript string literal. A file aboard doesn't have is empty;
// LoadProfile has checked that every profile's files are there.
func FillFile(source string, e Env) []byte {
	raw, err := adapters.Files.ReadFile(source)
	if err != nil {
		return nil
	}
	inside := func(s string) string {
		q, _ := json.Marshal(s)
		return string(q[1 : len(q)-1])
	}
	return []byte(strings.NewReplacer("{aboard_binary}", inside(e.Aboard), "{aboard_home}", inside(e.AboardHome)).Replace(string(raw)))
}
