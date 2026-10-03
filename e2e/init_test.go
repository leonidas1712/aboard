//go:build e2e

package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// harnessHome gives the env's home directory the folders that show Claude Code and Codex
// are installed.
func (e *env) harnessHome() {
	e.t.Helper()
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o755); err != nil {
			e.t.Fatal(err)
		}
	}
}

// filesUnder lists the files under dir, relative to it, leaving out the subdirectories
// named in skip.
func filesUnder(t *testing.T, dir string, skip ...string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && slices.Contains(skip, d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// aboard init --yes --scope project writes the skill, hooks and allow rules only under
// the project, a second run changes nothing, and status and doctor report the scope.
func TestInitProjectScopeWritesOnlyUnderTheProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()

	plan := e.run("init", "--scope", "project", "--allow-commands")
	if !strings.HasSuffix(plan.stdout, "Run aboard init --yes --scope project --allow-commands to make these changes.\n") {
		t.Fatalf("plan:\n%s", plan)
	}
	if got := filesUnder(t, e.dir); len(got) != 0 {
		t.Fatalf("init without --yes wrote %v", got)
	}

	r := e.run("init", "--yes", "--scope", "project", "--allow-commands", "--json").json(t)
	if field(t, r, "applied") != true || field(t, r, "scope") != "project" || field(t, r, "allow_commands") != true || field(t, r, "delivery") != nil {
		t.Fatalf("init: %v", r)
	}
	want := []string{
		".agents/skills/aboard/SKILL.md", ".claude/settings.local.json", ".claude/skills/aboard/SKILL.md",
		".codex/hooks.json", ".codex/rules/aboard.rules",
	}
	if got := filesUnder(t, e.dir); !slices.Equal(got, want) {
		t.Fatalf("project files %v, want %v", got, want)
	}
	if got := filesUnder(t, e.home, "project", "aboard"); len(got) != 0 {
		t.Fatalf("project setup wrote outside the project: %v", got)
	}
	settings, err := os.ReadFile(filepath.Join(e.dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{binary + " hook claude-code stop", `"Bash(aboard *)"`} {
		if !strings.Contains(string(settings), want) {
			t.Fatalf("Claude Code settings lack %s:\n%s", want, settings)
		}
	}

	again := e.run("init", "--yes", "--scope", "project", "--allow-commands", "--json").json(t)
	for _, h := range field(t, again, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if c.(map[string]any)["action"] != "unchanged" {
				t.Fatalf("second init changed %v", c)
			}
		}
	}

	if !strings.Contains(e.run("status").stdout, "Setup:  claude-code in this project, codex in this project\n") {
		t.Fatalf("status:\n%s", e.run("status"))
	}
	st := e.run("status", "--json").json(t)
	if g, p := field(t, st, "setup.global").([]any), field(t, st, "setup.project").([]any); len(g) != 0 || len(p) != 2 {
		t.Fatalf("status setup %v", field(t, st, "setup"))
	}
	doctor := e.runExit("doctor").stdout
	if !strings.Contains(doctor, "✓ claude-code: hooks installed in this project ("+filepath.Join(e.dir, ".claude", "settings.local.json")+")") {
		t.Fatalf("doctor:\n%s", doctor)
	}
}

// aboard init --delivery sets the mode of agents without their own, and is refused inside
// a harness session, where an agent would be choosing it.
func TestInitSetsTheDefaultDeliveryMode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("pair", "writer-reviewer")

	r := e.exec([]string{"CLAUDECODE=1"}, "", "init", "--yes", "--delivery", "off", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("want human_command_in_session\n%s", r)
	}

	out := e.run("init", "--yes", "--delivery", "off", "--json").json(t)
	if field(t, out, "delivery.mode") != "off" || field(t, out, "delivery.action") != "update" {
		t.Fatalf("init: %v", out)
	}
	expectLines(t, e.run("delivery", "--as", "writer"),
		"writer on writer-reviewer: delivery off (delivers nothing; the agent reads its inbox itself)")
	if got := field(t, e.run("init", "--yes", "--delivery", "off", "--json").json(t), "delivery.action"); got != "unchanged" {
		t.Fatalf("second init: %v", got)
	}
}

// Claude Code reads its config from CLAUDE_CONFIG_DIR and Codex from CODEX_HOME when they
// are set, so aboard init writes the global hooks and Claude Code's skill there, and
// doctor and status look there. Codex's global skill stays in ~/.agents/skills, which
// CODEX_HOME doesn't move.
func TestGlobalSetupFollowsTheHarnessConfigVariables(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	claudeDir, codexDir := filepath.Join(elsewhere, "claude"), filepath.Join(elsewhere, "codex")
	for _, d := range []string{claudeDir, codexDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vars := []string{"CLAUDE_CONFIG_DIR=" + claudeDir, "CODEX_HOME=" + codexDir}

	e.exec(vars, "", "init", "--yes", "--allow-commands")
	if got, want := filesUnder(t, elsewhere), []string{
		"claude/settings.json", "claude/skills/aboard/SKILL.md", "codex/hooks.json", "codex/rules/aboard.rules",
	}; !slices.Equal(got, want) {
		t.Fatalf("files under the config directories %v, want %v", got, want)
	}
	for _, d := range []string{".claude", ".codex"} {
		if _, err := os.Stat(filepath.Join(e.home, d)); !os.IsNotExist(err) {
			t.Fatalf("init wrote ~/%s although the harness reads its config elsewhere: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md")); err != nil {
		t.Fatalf("Codex's skill: %v", err)
	}

	doctor := e.exec(vars, "", "doctor").stdout
	for _, want := range []string{
		"✓ claude-code: hooks installed everywhere (" + filepath.Join(claudeDir, "settings.json") + ")",
		"✓ codex: hooks installed everywhere (" + filepath.Join(codexDir, "hooks.json") + ")",
		"✓ claude-code: skill installed in " + filepath.Join(claudeDir, "skills", "aboard", "SKILL.md"),
	} {
		if !strings.Contains(doctor, want) {
			t.Fatalf("doctor lacks %q:\n%s", want, doctor)
		}
	}
	if status := e.exec(vars, "", "status").stdout; !strings.Contains(status, "Setup:  claude-code everywhere, codex everywhere\n") {
		t.Fatalf("status:\n%s", status)
	}
	// Without the variables, the same machine has no global setup in the default places.
	if status := e.run("status").stdout; !strings.Contains(status, "Setup:  none") {
		t.Fatalf("status without the variables:\n%s", status)
	}
}

// aboard init --yes asks the person to trust hooks only in the harnesses whose hook files
// it added or updated: rewriting only the skill needs no new trust.
func TestInitAsksToTrustOnlyChangedHooks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	const both = "Done. Claude Code and Codex ask you to trust new hooks before they run: review them in /hooks in each."
	if out := e.run("init", "--yes").stdout; !strings.HasSuffix(out, both+"\n") {
		t.Fatalf("first init:\n%s", out)
	}

	skill := filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")
	if err := os.WriteFile(skill, []byte(readFile(t, skill)+"\nAn older paragraph.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := e.run("init", "--yes").stdout; !strings.HasSuffix(out, "\nDone.\n") || strings.Contains(out, "trust") {
		t.Fatalf("init that rewrote only the skill:\n%s", out)
	}

	hooks := filepath.Join(e.home, ".codex", "hooks.json")
	edited := strings.Replace(readFile(t, hooks), `"timeout": 30`, `"timeout": 31`, 1)
	if err := os.WriteFile(hooks, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	const codex = "Done. Codex asks you to trust new hooks before they run: review them in /hooks."
	if out := e.run("init", "--yes").stdout; !strings.HasSuffix(out, codex+"\n") {
		t.Fatalf("init that updated only Codex's hooks:\n%s", out)
	}
}

// Hooks installed by an earlier aboard, with the tool hook on the events it used then,
// are reported out of date until aboard init --yes moves them, leaving no Aboard entry
// on the old events. That aboard predates the install manifest, so there is none.
func TestDoctorReportsHooksFromBeforeTheToolHookMoved(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes")
	if err := os.Remove(e.manifestPath()); err != nil {
		t.Fatal(err)
	}
	claude, codex := filepath.Join(e.home, ".claude", "settings.json"), filepath.Join(e.home, ".codex", "hooks.json")
	for path, from := range map[string]string{claude: `"PostToolBatch"`, codex: `"PreToolUse"`} {
		old := strings.Replace(readFile(t, path), from, `"PostToolUse"`, 1)
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checks := func() map[string]string {
		got := map[string]string{}
		for _, c := range field(t, e.runExit("doctor", "--json").json(t), "checks").([]any) {
			m := c.(map[string]any)
			code, _ := m["code"].(string)
			got[m["name"].(string)] = code
		}
		return got
	}
	if got := checks(); got["claude_hooks"] != "hooks_outdated" || got["codex_hooks"] != "hooks_outdated" {
		t.Fatalf("doctor with the earlier hooks: %v", got)
	}
	e.run("init", "--yes")
	if got := checks(); got["claude_hooks"] != "" || got["codex_hooks"] != "" {
		t.Fatalf("doctor after init --yes: %v", got)
	}
	for _, path := range []string{claude, codex} {
		if text := readFile(t, path); strings.Contains(text, `"PostToolUse"`) {
			t.Fatalf("an Aboard hook stayed on PostToolUse in %s:\n%s", path, text)
		}
	}
}

// A Claude Code older than the PostToolBatch hook gets the tool hook on PostToolUse and
// PostToolUseFailure instead, which together fire after every tool call.
func TestInitFallsBackForAnOlderClaudeCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.vars = append(e.vars, "FAKE_CLAUDE_VERSION=2.1.110")
	e.run("init", "--yes", "--harness", "claude-code")
	settings := readFile(t, filepath.Join(e.home, ".claude", "settings.json"))
	for _, want := range []string{`"PostToolUse"`, `"PostToolUseFailure"`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("settings lack %s:\n%s", want, settings)
		}
	}
	if strings.Contains(settings, "PostToolBatch") {
		t.Fatalf("an older Claude Code got PostToolBatch:\n%s", settings)
	}
}
