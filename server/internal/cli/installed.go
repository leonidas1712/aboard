package cli

import (
	"bytes"
	"os"
	"path/filepath"

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

// checkSkill reports a harness's installed skill that differs from the one this aboard
// installs. It returns nothing when the skill isn't installed; the hooks check covers
// setup.
func checkSkill(name, harness, path string) []doctorCheck {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil
	}
	if !bytes.Equal(data, skill.Skill) {
		return []doctorCheck{problem(name, levelWarning, "skill_outdated",
			harness+": the skill in "+path+" differs from the one aboard "+version+" installs",
			"run aboard init --yes")}
	}
	return []doctorCheck{okCheck(name, harness+": skill installed in "+path)}
}

// checkHooksCurrent reports Aboard hook entries that differ from what aboard init would
// write now, or returns ok.
func checkHooksCurrent(name, harness, path string, specs []hookSpec, ok doctorCheck) doctorCheck {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return ok
	}
	if _, changed, err := mergeHooks(data, harness, specs); err != nil || !changed {
		return ok
	}
	return problem(name, levelWarning, "hooks_outdated",
		harness+": the Aboard hooks in "+path+" differ from the ones aboard "+version+" installs",
		"run aboard init --yes")
}
