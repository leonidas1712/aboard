//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// manifestPath is the install manifest aboard init writes in the env's state folder.
func (e *env) manifestPath() string { return filepath.Join(e.stateDir(), "installs.json") }

// manifestFile is one file the install manifest records.
type manifestFile struct {
	Path    string `json:"path"`
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// manifest reads the install manifest, by path.
func (e *env) manifest() map[string]manifestFile {
	e.t.Helper()
	var m struct {
		Files []manifestFile `json:"files"`
	}
	if err := json.Unmarshal([]byte(readFile(e.t, e.manifestPath())), &m); err != nil {
		e.t.Fatalf("read the install manifest: %v", err)
	}
	out := map[string]manifestFile{}
	for _, f := range m.Files {
		out[f.Path] = f
	}
	return out
}

// snapshot reads every file under dir, leaving out the subdirectories named in skip.
func snapshot(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range filesUnder(t, dir, skip...) {
		out[rel] = readFile(t, filepath.Join(dir, rel))
	}
	return out
}

// sameJSON fails unless the JSON in path means the same as want.
func sameJSON(t *testing.T, path, want string) {
	t.Helper()
	var got, exp any
	if err := json.Unmarshal([]byte(readFile(t, path)), &got); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("%s:\n%s\nwant the same as:\n%s", path, readFile(t, path), want)
	}
}

func gone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there (%v)", p, err)
		}
	}
}

// uninstallFiles returns the files an uninstall output lists, as "action kind harness path".
func uninstallFiles(t *testing.T, out map[string]any) []string {
	t.Helper()
	var got []string
	for _, f := range field(t, out, "files").([]any) {
		m := f.(map[string]any)
		got = append(got, m["action"].(string)+" "+m["kind"].(string)+" "+m["harness"].(string)+" "+m["path"].(string))
	}
	return got
}

// aboard init --yes records each file it wrote in the install manifest, with the
// harness, the scope, the version that wrote it and a hash of what it wrote.
func TestInitRecordsWhatItWroteInTheInstallManifest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes", "--allow-commands")

	m := e.manifest()
	skill := filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")
	want := map[string][2]string{
		skill: {"claude-code", "skill"},
		filepath.Join(e.home, ".claude", "settings.json"):                {"claude-code", "hooks"},
		filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md"): {"codex", "skill"},
		filepath.Join(e.home, ".codex", "hooks.json"):                    {"codex", "hooks"},
		filepath.Join(e.home, ".codex", "rules", "aboard.rules"):         {"codex", "permissions"},
	}
	if len(m) != len(want) {
		t.Fatalf("manifest %v, want the files %v", m, want)
	}
	for path, hk := range want {
		f, ok := m[path]
		if !ok || f.Harness != hk[0] || f.Kind != hk[1] || f.Scope != "global" || f.Version != "0.1.0" || len(f.SHA256) != 64 {
			t.Fatalf("manifest's record of %s: %+v", path, f)
		}
	}
	sum := sha256.Sum256([]byte(readFile(t, skill)))
	if m[skill].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("the skill's hash %s, want the file's %x", m[skill].SHA256, sum)
	}
}

// With the install manifest, doctor tells files an older aboard wrote and nobody touched
// (outdated, naming the version) from files the person edited, and init marks an edited
// file it would replace.
func TestDoctorTellsOutdatedFilesFromEditedOnes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	older := filepath.Join(e.home, "old", "aboard")
	installAt(t, oldBinary, older)
	e.bin = older
	e.run("init", "--yes")
	if v := e.manifest()[filepath.Join(e.home, ".codex", "hooks.json")].Version; v != oldVersion {
		t.Fatalf("manifest version %q, want %q", v, oldVersion)
	}

	// The new aboard is installed at another path, so the hook commands it writes differ.
	e.bin = binary
	codexHooks := filepath.Join(e.home, ".codex", "hooks.json")
	checks := e.doctorChecks()
	if c := checks["codex_hooks"]; c["code"] != "hooks_outdated" ||
		c["message"] != "codex: the Aboard hooks in "+codexHooks+" were written by aboard "+oldVersion+" and differ from the ones aboard 0.1.0 installs" {
		t.Fatalf("codex_hooks: %v", c)
	}

	settings := filepath.Join(e.home, ".claude", "settings.json")
	edited := strings.Replace(readFile(t, settings), `"timeout": 30`, `"timeout": 31`, 1)
	if err := os.WriteFile(settings, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	codexSkill := filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md")
	if err := os.WriteFile(codexSkill, []byte(readFile(t, codexSkill)+"\nMy own note.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks = e.doctorChecks()
	for name, want := range map[string]string{
		"claude_hooks": "hooks_edited", "codex_hooks": "hooks_outdated",
		"codex_skill": "skill_edited", "claude_skill": "",
	} {
		if got, _ := checks[name]["code"].(string); got != want {
			t.Fatalf("doctor's %s check: %v, want code %q", name, checks[name], want)
		}
	}
	if msg := checks["codex_skill"]["message"]; msg != "codex: the skill in "+codexSkill+" was edited after aboard "+oldVersion+" wrote it" {
		t.Fatalf("codex_skill message: %v", msg)
	}

	plan := e.run("init", "--json").json(t)
	matchesCLISpec(t, "InitOutput", plan)
	marked := map[string]any{}
	for _, h := range field(t, plan, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			m := c.(map[string]any)
			if m["edited"] == true {
				marked[m["path"].(string)] = m["written_by"]
			}
		}
	}
	if want := map[string]any{settings: oldVersion, codexSkill: oldVersion}; !reflect.DeepEqual(marked, want) {
		t.Fatalf("init marked as edited %v, want %v", marked, want)
	}
	if text := e.run("init").stdout; !strings.Contains(text, "~/.agents/skills/aboard/SKILL.md (skill; edited since aboard "+oldVersion+" wrote it)") {
		t.Fatalf("init's plan:\n%s", text)
	}

	e.run("init", "--yes")
	checks = e.doctorChecks()
	for _, name := range []string{"claude_hooks", "codex_hooks", "claude_skill", "codex_skill"} {
		if checks[name]["level"] != "ok" {
			t.Fatalf("after init --yes, doctor's %s check: %v", name, checks[name])
		}
	}
	if v := e.manifest()[codexHooks].Version; v != "0.1.0" {
		t.Fatalf("manifest version after init --yes %q, want 0.1.0", v)
	}
}

// aboard uninstall --dry-run lists what it would do and changes nothing: the files, the
// manifest, and the running server and daemon stay as they were.
func TestUninstallDryRunChangesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes", "--allow-commands")
	e.run("pair")
	e.run("doctor") // starts the delivery daemon
	before, manifest := snapshot(t, e.home, "aboard"), readFile(t, e.manifestPath())

	out := e.run("uninstall", "--dry-run", "--json").json(t)
	matchesCLISpec(t, "UninstallOutput", out)
	if field(t, out, "applied") != false || field(t, out, "server_stopped") != true || field(t, out, "daemon_stopped") != true ||
		field(t, out, "data.delete") != false || field(t, out, "data.deleted") != false {
		t.Fatalf("dry run: %v", out)
	}
	want := []string{
		"delete skill claude-code " + filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md"),
		"delete hooks claude-code " + filepath.Join(e.home, ".claude", "settings.json"),
		"delete skill codex " + filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md"),
		"delete hooks codex " + filepath.Join(e.home, ".codex", "hooks.json"),
		"delete permissions codex " + filepath.Join(e.home, ".codex", "rules", "aboard.rules"),
	}
	if got := uninstallFiles(t, out); !slices.Equal(got, want) {
		t.Fatalf("dry run files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	exe, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	expectLines(t, e.run("uninstall", "--dry-run"),
		"Would stop local Aboard at http://"+e.addr+" and the delivery daemon.",
		"delete   ~/.claude/skills/aboard/SKILL.md (claude-code skill)",
		"delete   ~/.claude/settings.json (claude-code hooks: it held only Aboard's entries)",
		"delete   ~/.agents/skills/aboard/SKILL.md (codex skill)",
		"delete   ~/.codex/hooks.json (codex hooks: it held only Aboard's entries)",
		"delete   ~/.codex/rules/aboard.rules (codex permissions)",
		"Kept Aboard's data in ~/aboard; aboard uninstall --data deletes it.",
		"aboard uninstall leaves the binary at "+exe+". Remove it with: rm "+exe,
		"Run aboard uninstall to make these changes.",
	)

	if after := snapshot(t, e.home, "aboard"); !reflect.DeepEqual(after, before) {
		t.Fatalf("a dry run changed files under the home directory")
	}
	if readFile(t, e.manifestPath()) != manifest {
		t.Fatalf("a dry run changed the install manifest")
	}
	st := e.run("status", "--json").json(t)
	if field(t, st, "server_running") != true || field(t, st, "daemon.running") != true {
		t.Fatalf("a dry run stopped something: %v", st)
	}
}

// aboard uninstall takes only Aboard's entries out of settings files the person also
// uses, deletes the files Aboard owns, stops the server and daemon, and keeps the data.
func TestUninstallKeepsThePersonsOwnSettings(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	const claudeOwn = `{
  "theme": "dark",
  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo done"}]}]},
  "permissions": {"allow": ["Bash(ls *)"]}
}`
	const codexOwn = `{"hooks": {"SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "say hi"}]}]}}`
	settings, codexHooks := filepath.Join(e.home, ".claude", "settings.json"), filepath.Join(e.home, ".codex", "hooks.json")
	if err := os.WriteFile(settings, []byte(claudeOwn), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHooks, []byte(codexOwn), 0o600); err != nil {
		t.Fatal(err)
	}
	e.run("init", "--yes", "--allow-commands")
	e.run("pair")
	if !strings.Contains(readFile(t, settings), "hook claude-code stop") || !strings.Contains(readFile(t, settings), "Bash(aboard *)") {
		t.Fatalf("init didn't add its entries:\n%s", readFile(t, settings))
	}

	out := e.run("uninstall", "--json").json(t)
	matchesCLISpec(t, "UninstallOutput", out)
	if field(t, out, "applied") != true || field(t, out, "server_stopped") != true {
		t.Fatalf("uninstall: %v", out)
	}
	sameJSON(t, settings, claudeOwn)
	sameJSON(t, codexHooks, codexOwn)
	gone(t, filepath.Join(e.home, ".claude", "skills", "aboard"), filepath.Join(e.home, ".agents", "skills", "aboard"),
		filepath.Join(e.home, ".codex", "rules", "aboard.rules"), e.manifestPath())
	for _, d := range []string{e.configDir(), e.dataDir()} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("uninstall without --data removed %s: %v", d, err)
		}
	}
	exe, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if field(t, out, "binary.path") != exe || field(t, out, "binary.remove") != "rm "+exe {
		t.Fatalf("binary: %v", field(t, out, "binary"))
	}
	st := e.run("status", "--json").json(t)
	if field(t, st, "server_running") != false || len(field(t, st, "setup.global").([]any)) != 0 {
		t.Fatalf("status after uninstall: %v", st)
	}

	again := e.run("uninstall", "--json").json(t)
	if files := field(t, again, "files").([]any); len(files) != 0 {
		t.Fatalf("a second uninstall found %v", files)
	}
	if text := e.run("uninstall").stdout; !strings.Contains(text, "No Aboard files found in harness settings.\n") {
		t.Fatalf("second uninstall:\n%s", text)
	}
}

// A skill the person edited after Aboard wrote it is kept, and uninstall says so; the
// manifest keeps its record so a later uninstall still knows it.
func TestUninstallKeepsAnEditedSkill(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes")
	skill := filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")
	mine := readFile(t, skill) + "\nMy own note.\n"
	if err := os.WriteFile(skill, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	r := e.run("uninstall")
	if !strings.Contains(r.stdout, "keep     ~/.claude/skills/aboard/SKILL.md (claude-code skill: edited since aboard 0.1.0 wrote it; remove it yourself if you don't need it)\n") {
		t.Fatalf("uninstall:\n%s", r)
	}
	if readFile(t, skill) != mine {
		t.Fatalf("the edited skill changed")
	}
	gone(t, filepath.Join(e.home, ".agents", "skills", "aboard"))
	if m := e.manifest(); len(m) != 1 || m[skill].Kind != "skill" {
		t.Fatalf("manifest after uninstall: %v", m)
	}
	out := e.run("uninstall", "--json").json(t)
	if got := uninstallFiles(t, out); !slices.Equal(got, []string{"keep skill claude-code " + skill}) || field(t, out, "files.0.reason") != "edited" {
		t.Fatalf("second uninstall: %v", out)
	}
}

// Data goes only with --data, which needs a yes: --yes when nobody can be asked, and a
// person's, never an agent's.
func TestUninstallDeletesDataOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes")
	e.run("pair")

	r := e.runExit("uninstall", "--data", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "confirmation_required" ||
		!strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard uninstall --data --yes") {
		t.Fatalf("want confirmation_required\n%s", r)
	}
	r = e.exec([]string{"CLAUDECODE=1"}, "", "uninstall", "--data", "--yes", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("want human_command_in_session\n%s", r)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")); err != nil {
		t.Fatalf("a refused uninstall changed files: %v", err)
	}

	e.run("uninstall")
	for _, d := range []string{e.configDir(), e.dataDir(), e.stateDir()} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("uninstall without --data removed %s: %v", d, err)
		}
	}
	out := e.run("uninstall", "--data", "--yes", "--json").json(t)
	matchesCLISpec(t, "UninstallOutput", out)
	if field(t, out, "data.delete") != true || field(t, out, "data.deleted") != true || len(field(t, out, "data.dirs").([]any)) != 3 {
		t.Fatalf("uninstall --data: %v", out)
	}
	gone(t, e.aboardHome())
}

// After an uninstall, with or without the data, init and pair work again from scratch.
func TestUninstallThenInitAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes")
	e.run("pair")
	e.run("uninstall", "--data", "--yes")

	e.run("init", "--yes")
	if _, ok := e.manifest()[filepath.Join(e.home, ".claude", "settings.json")]; !ok {
		t.Fatalf("init after uninstall didn't record the hooks")
	}
	checks := e.doctorChecks()
	for _, name := range []string{"claude_hooks", "codex_hooks", "claude_skill", "codex_skill"} {
		if checks[name]["level"] != "ok" {
			t.Fatalf("doctor's %s check after reinstalling: %v", name, checks[name])
		}
	}
	e.run("pair", "--new")
}

// Installs from before the manifest are found by content, in both scopes, and a project
// install the manifest records is removed from anywhere.
func TestUninstallFindsInstallsWithAndWithoutTheManifest(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	e.run("init", "--yes")
	if err := os.Remove(e.manifestPath()); err != nil {
		t.Fatal(err)
	}
	e.run("init", "--yes", "--scope", "project", "--allow-commands")

	e.dir = e.home // run from elsewhere: the project is known only from the manifest
	out := e.run("uninstall", "--json").json(t)
	project := filepath.Join(e.home, "project")
	want := []string{
		"delete skill claude-code " + filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md"),
		"delete hooks claude-code " + filepath.Join(e.home, ".claude", "settings.json"),
		"delete skill claude-code " + filepath.Join(project, ".claude", "skills", "aboard", "SKILL.md"),
		"delete hooks claude-code " + filepath.Join(project, ".claude", "settings.local.json"),
		"delete skill codex " + filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md"),
		"delete hooks codex " + filepath.Join(e.home, ".codex", "hooks.json"),
		"delete skill codex " + filepath.Join(project, ".agents", "skills", "aboard", "SKILL.md"),
		"delete hooks codex " + filepath.Join(project, ".codex", "hooks.json"),
		"delete permissions codex " + filepath.Join(project, ".codex", "rules", "aboard.rules"),
	}
	if got := uninstallFiles(t, out); !slices.Equal(got, want) {
		t.Fatalf("uninstall files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range field(t, out, "files").([]any) {
		m := f.(map[string]any)
		inProject := strings.HasPrefix(m["path"].(string), project)
		if (m["scope"] == "project") != inProject || (m["written_by"] == nil) != !inProject {
			t.Fatalf("file %v: want scope project and a version exactly for the project's files", m)
		}
	}
	if got := filesUnder(t, e.home, "aboard"); len(got) != 0 {
		t.Fatalf("files left after uninstall: %v", got)
	}
}
