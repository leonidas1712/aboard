//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The commands on docs/install.mdx, in the page's order, each with the check the page
// tells an agent to run. Building from source and removing the binary are steps in
// e2e/RELEASE_CHECKLIST.md.
func TestInstallPageCommands(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	settings := filepath.Join(e.home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"theme": "dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Install: aboard version --json.
	v := e.run("version", "--json").json(t)
	matchesCLISpec(t, "VersionOutput", v)
	if field(t, v, "version") != "0.1.0" {
		t.Fatalf("version: %v", v)
	}

	// Set up your harnesses: aboard init --yes, then aboard doctor --json.
	e.run("init", "--yes")
	doctor := e.runExit("doctor", "--json").json(t)
	matchesCLISpec(t, "DoctorOutput", doctor)
	checks := e.doctorChecks()
	for _, name := range []string{"claude_hooks", "claude_skill", "codex_hooks", "codex_skill"} {
		if checks[name]["level"] != "ok" {
			t.Fatalf("doctor's %s check: %v", name, checks[name])
		}
	}

	// Update: aboard init --yes again changes nothing when the build writes the same.
	if out := e.run("init", "--yes").stdout; !strings.HasSuffix(out, "Nothing to change.\n") {
		t.Fatalf("init --yes again:\n%s", out)
	}

	// Stop: aboard down --json.
	e.run("pair")
	down := e.run("down", "--json").json(t)
	matchesCLISpec(t, "DownOutput", down)
	if field(t, down, "server_stopped") != true {
		t.Fatalf("down: %v", down)
	}

	// Remove: aboard uninstall --dry-run, aboard uninstall --json, aboard uninstall --data --yes.
	e.run("pair", "--new")
	e.run("doctor") // starts the delivery daemon
	exe, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	expectLines(t, e.run("uninstall", "--dry-run"),
		"Would stop local Aboard at http://"+e.addr+" and the delivery daemon.",
		"delete   ~/.claude/skills/aboard/SKILL.md (claude-code skill)",
		"edit     ~/.claude/settings.json (claude-code hooks: took out Aboard's entries, kept the rest)",
		"delete   ~/.agents/skills/aboard/SKILL.md (codex skill)",
		"delete   ~/.codex/hooks.json (codex hooks: it held only Aboard's entries)",
		"Kept Aboard's data in ~/aboard; aboard uninstall --data deletes it.",
		"aboard uninstall leaves the binary at "+exe+". Remove it with: rm "+exe,
		"Run aboard uninstall to make these changes.",
	)
	out := e.run("uninstall", "--json").json(t)
	matchesCLISpec(t, "UninstallOutput", out)
	sameJSON(t, settings, `{"theme": "dark"}`)
	e.run("uninstall", "--data", "--yes")
	gone(t, e.aboardHome())

	// The check that nothing is left.
	st := e.run("status", "--json").json(t)
	matchesCLISpec(t, "StatusOutput", st)
	if len(field(t, st, "setup.global").([]any)) != 0 || len(field(t, st, "setup.project").([]any)) != 0 {
		t.Fatalf("status setup after uninstall: %v", field(t, st, "setup"))
	}
	if files := field(t, e.run("uninstall", "--json").json(t), "files").([]any); len(files) != 0 {
		t.Fatalf("uninstall found %v after removing everything", files)
	}
}
