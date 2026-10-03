//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	skill "github.com/leonidas1712/aboard/skills/aboard"
)

// updateGolden rewrites the golden files instead of comparing with them:
// ABOARD_UPDATE_GOLDEN=1 go test -tags e2e -run TestInitWritesExactlyTheGoldenFiles ./e2e/
var updateGolden = os.Getenv("ABOARD_UPDATE_GOLDEN") != ""

// goldenCase is one aboard init run, with the files a person had before it.
type goldenCase struct {
	name string
	args []string
	// vars are extra environment variables for every command.
	vars []string
	// seed holds files the person already had, by path under the home directory.
	seed map[string]string
	// before are init runs made first, with --yes added.
	before [][]string
}

// personSettings and personHooks are a person's own Claude Code settings and Codex
// hooks, which aboard init must keep as they are and aboard uninstall must leave.
const (
	personSettings = `{
  "model": "opus",
  "permissions": {
    "allow": [
      "Bash(git status)"
    ]
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "say done"
          }
        ]
      }
    ]
  }
}
`
	personHooks = `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup",
        "hooks": [
          {
            "type": "command",
            "command": "echo hello"
          }
        ]
      }
    ]
  }
}
`
	personRules = "prefix_rule(pattern=[\"git\", \"status\"], decision=\"allow\")\n"
)

// aboard init writes exactly the files recorded in e2e/testdata/golden, byte for byte,
// for each harness, scope and option, and says exactly what it did; doctor and status
// read them back; and aboard uninstall takes out only what init added, leaving the
// person's own files as they were. The hook entries' text matters most: when it
// changes, the harnesses ask the person to trust the hooks again.
func TestInitWritesExactlyTheGoldenFiles(t *testing.T) {
	t.Parallel()
	cases := []goldenCase{
		{name: "claude-code-global", args: []string{"--harness", "claude-code"}},
		{name: "claude-code-global-allow", args: []string{"--harness", "claude-code", "--allow-commands"}},
		{name: "claude-code-project", args: []string{"--harness", "claude-code", "--scope", "project"}},
		{name: "claude-code-project-allow", args: []string{"--harness", "claude-code", "--scope", "project", "--allow-commands"}},
		{name: "codex-global", args: []string{"--harness", "codex"}},
		{name: "codex-global-allow", args: []string{"--harness", "codex", "--allow-commands"}},
		{name: "codex-project", args: []string{"--harness", "codex", "--scope", "project"}},
		{name: "codex-project-allow", args: []string{"--harness", "codex", "--scope", "project", "--allow-commands"}},
		{name: "both-global-seeded-allow", args: []string{"--allow-commands"}, seed: map[string]string{
			".claude/settings.json":      personSettings,
			".codex/hooks.json":          personHooks,
			".codex/rules/default.rules": personRules,
		}},
		{name: "both-project-seeded-allow", args: []string{"--scope", "project", "--allow-commands"}, seed: map[string]string{
			"project/.claude/settings.local.json": personSettings,
			"project/.codex/hooks.json":           personHooks,
		}},
		{name: "claude-code-older-version", args: []string{"--harness", "claude-code"}, vars: []string{"FAKE_CLAUDE_VERSION=2.1.100"}},
		{
			name: "both-config-dirs-allow", args: []string{"--allow-commands"},
			vars: []string{"CLAUDE_CONFIG_DIR={HOME}/claude-config", "CODEX_HOME={HOME}/codex-home"},
		},
		{name: "both-project-after-global", args: []string{"--scope", "project"}, before: [][]string{{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.harnessHome()
			for _, v := range c.vars {
				e.vars = append(e.vars, strings.ReplaceAll(v, "{HOME}", e.home))
			}
			for rel, content := range c.seed {
				p := filepath.Join(e.home, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // a test file
					t.Fatal(err)
				}
			}
			start := goldenFiles(t, e)
			for _, args := range c.before {
				e.run(append([]string{"init", "--yes"}, args...)...)
			}

			var b strings.Builder
			section := func(title, body string) {
				b.WriteString("==== " + title + " ====\n" + body)
				if !strings.HasSuffix(body, "\n") {
					b.WriteString("\n(no newline at end)\n")
				}
			}
			section("aboard init "+strings.Join(c.args, " "), e.run(append([]string{"init"}, c.args...)...).stdout)
			section("aboard init --yes "+strings.Join(c.args, " ")+" --json",
				e.run(append(append([]string{"init", "--yes"}, c.args...), "--json")...).stdout)
			section("aboard init --yes "+strings.Join(c.args, " ")+" (again)",
				e.run(append([]string{"init", "--yes"}, c.args...)...).stdout)
			after := goldenFiles(t, e)
			for _, rel := range sortedKeys(after) {
				content := after[rel]
				if content == string(skill.Skill) {
					content = "(the Aboard skill this aboard installs, byte for byte)\n"
				}
				section("file "+rel, content)
			}
			if m, err := os.ReadFile(e.manifestPath()); err == nil {
				section("install manifest", string(m))
			}
			status := e.run("status", "--json").json(t)
			setup, _ := json.MarshalIndent(status["setup"], "", "  ")
			section("aboard status --json: setup", string(setup)+"\n")
			for _, line := range e.run("status").lines() {
				if strings.HasPrefix(line, "Setup:") {
					section("aboard status: setup line", line+"\n")
				}
			}
			var checks []any
			for _, ch := range field(t, e.runExit("doctor", "--json").json(t), "checks").([]any) {
				if name := ch.(map[string]any)["name"].(string); strings.HasPrefix(name, "claude") || strings.HasPrefix(name, "codex") {
					checks = append(checks, ch)
				}
			}
			doctor, _ := json.MarshalIndent(checks, "", "  ")
			section("aboard doctor --json: harness checks", string(doctor)+"\n")
			un := e.run("uninstall", "--json").json(t)
			files, _ := json.MarshalIndent(un["files"], "", "  ")
			section("aboard uninstall --json: files", string(files)+"\n")

			// What uninstall leaves is exactly what the person had before init.
			left := goldenFiles(t, e)
			if !mapsEqual(left, start) {
				for _, rel := range sortedKeys(left) {
					section("after uninstall: file "+rel, left[rel])
				}
				t.Errorf("aboard uninstall didn't leave the files as they were before aboard init:\nbefore %v\nafter %v", sortedKeys(start), sortedKeys(left))
			}

			got := normalizeGolden(e, b.String())
			path := filepath.Join("testdata", "golden", c.name+".txt")
			if updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // a test fixture
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the golden file: %v (ABOARD_UPDATE_GOLDEN=1 writes it)", err)
			}
			if got != string(want) {
				t.Fatalf("output differs from %s at line %d:\n%s", path, firstDiffLine(got, string(want)), got)
			}
		})
	}
}

// goldenFiles reads every file a harness setup can touch: under the home directory,
// leaving out Aboard's own folder and the fake Codex's records.
func goldenFiles(t *testing.T, e *env) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range filesUnder(t, e.home) {
		if !strings.HasPrefix(rel, "fake-codex-") && !strings.HasPrefix(rel, "aboard/") {
			out[rel] = readFile(t, filepath.Join(e.home, rel))
		}
	}
	return out
}

var (
	goldenHash    = regexp.MustCompile(`"sha256": "[0-9a-f]{64}"`)
	goldenVersion = regexp.MustCompile(`"(version|written_by)": "[^"]*"`)
)

// normalizeGolden replaces what differs between runs: the paths of the binary, the
// Aboard home and the home directory, and the manifest's hashes and versions.
func normalizeGolden(e *env, s string) string {
	s = strings.ReplaceAll(s, e.aboardHome(), "{ABOARD_HOME}")
	s = strings.ReplaceAll(s, binary, "{ABOARD}")
	s = strings.ReplaceAll(s, e.home, "{HOME}")
	s = goldenHash.ReplaceAllString(s, `"sha256": "{sha256}"`)
	return goldenVersion.ReplaceAllString(s, `"$1": "{version}"`)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// firstDiffLine returns the first line number, from 1, where a and b differ.
func firstDiffLine(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			return i + 1
		}
	}
	return min(len(al), len(bl)) + 1
}
