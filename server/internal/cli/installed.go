package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	skill "github.com/leonidas1712/aboard/skills/aboard"
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
func (a *app) checkSkill(name, harness string) []doctorCheck {
	var installed []string
	for _, scope := range a.scopes() {
		path := a.setupFiles(harness, scope).skill
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			continue
		}
		if !bytes.Equal(data, skill.Skill) {
			by, edited, known := a.loadManifest().fileOrigin(path, "skill", harness, data)
			switch {
			case edited:
				return []doctorCheck{problem(name, levelWarning, "skill_edited",
					harness+": the skill in "+path+" was edited after aboard "+by+" wrote it",
					initFix(scope)+" to replace it with the one aboard "+version+" installs, which discards your edits; or keep it as it is")}
			case known:
				return []doctorCheck{problem(name, levelWarning, "skill_outdated",
					harness+": the skill in "+path+" was written by aboard "+by+" and differs from the one aboard "+version+" installs",
					initFix(scope))}
			}
			return []doctorCheck{problem(name, levelWarning, "skill_outdated",
				harness+": the skill in "+path+" differs from the one aboard "+version+" installs",
				initFix(scope))}
		}
		installed = append(installed, path)
	}
	if len(installed) == 0 {
		return nil
	}
	return []doctorCheck{okCheck(name, harness+": skill installed in "+strings.Join(installed, " and "))}
}

// checkHooksCurrent reports Aboard hook entries, in each of the scopes given, that
// differ from what aboard init would write now, or returns ok.
func (a *app) checkHooksCurrent(name, harness string, scopes []string, specs []hookSpec, ok doctorCheck) doctorCheck {
	for _, scope := range scopes {
		path := a.setupFiles(harness, scope).hooks
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			continue
		}
		if _, changed, err := mergeHooks(data, harness, specs); err != nil || !changed {
			continue
		}
		by, edited, known := a.loadManifest().fileOrigin(path, "hooks", harness, data)
		switch {
		case edited:
			return problem(name, levelWarning, "hooks_edited",
				harness+": the Aboard hooks in "+path+" were edited after aboard "+by+" wrote them",
				initFix(scope)+", which rewrites only Aboard's entries")
		case known:
			return problem(name, levelWarning, "hooks_outdated",
				harness+": the Aboard hooks in "+path+" were written by aboard "+by+" and differ from the ones aboard "+version+" installs",
				initFix(scope))
		}
		return problem(name, levelWarning, "hooks_outdated",
			harness+": the Aboard hooks in "+path+" differ from the ones aboard "+version+" installs",
			initFix(scope))
	}
	return ok
}
