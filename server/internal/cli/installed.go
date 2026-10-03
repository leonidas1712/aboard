package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Installed files are compared by content with what aboard init would write now, not
// marked with a version: hook entries that stay the same across an upgrade are never
// rewritten, so the harnesses never ask to trust them again.

// hookExe returns the path hook commands run aboard by: this binary's absolute path, as
// aboard init writes it.
func (a *app) hookExe() string {
	exe, err := a.env.Executable()
	if err != nil {
		return "aboard"
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs
	}
	return exe
}

// initFix is the command that brings a scope's installed files up to date.
func initFix(scope string) string {
	if scope == scopeProject {
		return "run aboard init --yes --scope project in this project"
	}
	return "run aboard init --yes"
}

// checkSkill reports a harness's installed skill, in each scope that has one, that
// differs from the one this aboard installs. It returns nothing when the skill isn't
// installed in any scope; the hooks check covers setup.
func (a *app) checkSkill(h harness.Harness) []doctorCheck {
	name, harnessName := h.Profile().CheckName+"_skill", h.Profile().Harness
	var installed []string
	for _, scope := range a.scopes() {
		s := a.skillState(h, scope)
		switch s.State {
		case stateMissing:
			continue
		case stateCurrent:
			installed = append(installed, s.Path)
			continue
		case stateEdited:
			return []doctorCheck{problem(name, levelWarning, "skill_edited",
				harnessName+": the skill in "+s.Path+" was edited after aboard "+s.WrittenBy+" wrote it",
				initFix(scope)+" to replace it with the one aboard "+version+" installs, which discards your edits; or keep it as it is")}
		}
		if s.WrittenBy != "" {
			return []doctorCheck{problem(name, levelWarning, "skill_outdated",
				harnessName+": the skill in "+s.Path+" was written by aboard "+s.WrittenBy+" and differs from the one aboard "+version+" installs",
				initFix(scope))}
		}
		return []doctorCheck{problem(name, levelWarning, "skill_outdated",
			harnessName+": the skill in "+s.Path+" differs from the one aboard "+version+" installs",
			initFix(scope))}
	}
	if len(installed) == 0 {
		return nil
	}
	return []doctorCheck{okCheck(name, harnessName+": skill installed in "+strings.Join(installed, " and "))}
}

// checkHooksCurrent reports Aboard hook entries, in each of the scopes given, that
// differ from what aboard init would write now, or returns ok.
func (a *app) checkHooksCurrent(name string, h harness.Harness, scopes []string, specs []harness.Hook, ok doctorCheck) doctorCheck {
	harnessName := h.Profile().Harness
	for _, scope := range scopes {
		s := a.installedHooksState(a.hooksFile(h, scope), harnessName, specs)
		switch s.State {
		case stateCurrent:
			continue
		case stateEdited:
			return problem(name, levelWarning, "hooks_edited",
				harnessName+": the Aboard hooks in "+s.Path+" were edited after aboard "+s.WrittenBy+" wrote them",
				initFix(scope)+", which rewrites only Aboard's entries")
		}
		if s.WrittenBy != "" {
			return problem(name, levelWarning, "hooks_outdated",
				harnessName+": the Aboard hooks in "+s.Path+" were written by aboard "+s.WrittenBy+" and differ from the ones aboard "+version+" installs",
				initFix(scope))
		}
		return problem(name, levelWarning, "hooks_outdated",
			harnessName+": the Aboard hooks in "+s.Path+" differ from the ones aboard "+version+" installs",
			initFix(scope))
	}
	return ok
}

// How a file aboard init writes compares with what this aboard would write there.
const (
	stateMissing  = "missing"  // not there, or without Aboard's part
	stateCurrent  = "current"  // what this aboard writes
	stateOutdated = "outdated" // different, and unchanged since an aboard wrote it, or no record says
	stateEdited   = "edited"   // changed after an aboard wrote it
)

// fileState is the state of one installed file. WrittenBy is the version of aboard the
// install manifest says last wrote it, or "" when it has no record.
type fileState struct {
	Path, State, WrittenBy string
}

// skillState compares a harness's skill in a scope with the one this aboard installs.
func (a *app) skillState(h harness.Harness, scope string) fileState {
	it, _ := a.item(h, scope, harness.ItemSkill)
	s := fileState{Path: it.Path, State: stateMissing}
	if it.Path == "" {
		return s
	}
	data, err := os.ReadFile(filepath.Clean(it.Path))
	switch {
	case err != nil:
		return s
	case bytes.Equal(data, it.Data):
		s.State = stateCurrent
		return s
	}
	return a.origin(s, "skill", h.Profile().Harness, data)
}

// hooksState compares a harness's Aboard hooks in a scope with specs. They count as
// installed when the session-start hook, which every other hook relies on, is there.
func (a *app) hooksState(h harness.Harness, scope string, specs []harness.Hook) fileState {
	path := a.hooksFile(h, scope)
	if missing, err := hooksMissing(path, h.Profile().Harness, sessionStartHooks(h, specs)); err != nil || len(missing) > 0 {
		return fileState{Path: path, State: stateMissing}
	}
	return a.installedHooksState(path, h.Profile().Harness, specs)
}

// installedHooksState compares the Aboard hooks in a file that has them with specs. A
// file that can't be read or merged counts as current here; other checks report it.
func (a *app) installedHooksState(path, harnessName string, specs []harness.Hook) fileState {
	s := fileState{Path: path, State: stateCurrent}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return s
	}
	if _, changed, err := mergeHooks(data, harnessName, specs); err != nil || !changed {
		return s
	}
	return a.origin(s, "hooks", harnessName, data)
}

// origin marks a file that differs from what this aboard writes as edited or outdated,
// with the version that wrote it, from the install manifest.
func (a *app) origin(s fileState, kind, harnessName string, data []byte) fileState {
	by, edited, known := a.loadManifest().fileOrigin(s.Path, kind, harnessName, data)
	s.State = stateOutdated
	if edited {
		s.State = stateEdited
	}
	if known {
		s.WrittenBy = by
	}
	return s
}

// currentHooks returns the hooks this aboard installs for a harness.
func (a *app) currentHooks(ctx context.Context, h harness.Harness) []harness.Hook {
	return a.withHome(h.Hooks(a.hookExe(), h.Version(ctx, a.henv())))
}
