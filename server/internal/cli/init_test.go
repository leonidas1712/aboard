package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/harness"
	"github.com/leonidas1712/aboard/server/internal/harness/registry"
)

const exe = "/usr/local/bin/aboard"

// hooksOf returns the hooks aboard init installs for the newest version of a harness,
// each running bin.
func hooksOf(t *testing.T, name, bin string) []harness.Hook {
	t.Helper()
	return harnessNamed(t, name).Hooks(bin, harness.Newest)
}

func harnessNamed(t *testing.T, name string) harness.Harness {
	t.Helper()
	h, ok := registry.Harnesses().Get(name)
	if !ok {
		t.Fatalf("no harness %s", name)
	}
	return h
}

// hookEntry is one hook entry as a harness's settings file holds it.
type hookEntry struct {
	Type        string `json:"type"`
	Command     string `json:"command"`
	Timeout     int    `json:"timeout"`
	AsyncRewake bool   `json:"asyncRewake"`
}

func TestMergeHooksKeepsEverythingElse(t *testing.T) {
	settings := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls:*)"], "deny": []},
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "say done"}]}],
    "Notification": [{"hooks": [{"type": "command", "command": "notify"}]}]
  },
  "zeta": 1
}`
	out, changed, err := mergeHooks([]byte(settings), "claude-code", hooksOf(t, "claude-code", exe))
	if err != nil || !changed {
		t.Fatalf("merge: changed %v, %v", changed, err)
	}
	text := string(out)
	for _, want := range []string{`"say done"`, `"notify"`, `"Bash(ls:*)"`, `"opus"`, exe + " hook claude-code stop", `"asyncRewake": true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("merged settings lack %s:\n%s", want, text)
		}
	}
	if strings.Index(text, `"model"`) > strings.Index(text, `"permissions"`) || strings.Index(text, `"hooks"`) > strings.Index(text, `"zeta"`) {
		t.Fatalf("keys were reordered:\n%s", text)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []hookEntry `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if n := len(parsed.Hooks["Stop"]); n != 2 {
		t.Fatalf("Stop groups %d, want the existing one and Aboard's", n)
	}
	stop := parsed.Hooks["Stop"][1].Hooks[0]
	if !stop.AsyncRewake || stop.Timeout != 86400 {
		t.Fatalf("stop hook %+v", stop)
	}

	again, changed, err := mergeHooks(out, "claude-code", hooksOf(t, "claude-code", exe))
	if err != nil || changed || string(again) != text {
		t.Fatalf("a second merge changed the file (changed %v, %v)", changed, err)
	}
}

func TestMergeHooksUpdatesAMovedBinaryInPlace(t *testing.T) {
	first, _, err := mergeHooks(nil, "codex", hooksOf(t, "codex", "/old/place/aboard"))
	if err != nil {
		t.Fatal(err)
	}
	moved, changed, err := mergeHooks(first, "codex", hooksOf(t, "codex", exe))
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	if strings.Contains(string(moved), "/old/place") || strings.Count(string(moved), "hook codex tool") != 1 {
		t.Fatalf("the old hook wasn't replaced in place:\n%s", moved)
	}
	if !strings.Contains(string(moved), `"additionalContextLimit": 8192`) {
		t.Fatalf("codex tool hook lacks its context limit:\n%s", moved)
	}
}

func TestMergeHooksRefusesAFileThatIsNotAnObject(t *testing.T) {
	if _, _, err := mergeHooks([]byte(`["not", "settings"]`), "claude-code", hooksOf(t, "claude-code", exe)); err == nil {
		t.Fatal("merged into a JSON array")
	}
}

func TestShellWordQuotesOnlyWhenNeeded(t *testing.T) {
	tests := map[string]string{
		"/usr/local/bin/aboard": "/usr/local/bin/aboard",
		"/Users/a b/bin/aboard": "'/Users/a b/bin/aboard'",
		"/Users/it's/aboard":    `'/Users/it'\''s/aboard'`,
	}
	for in, want := range tests {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
	if !isAboardHook("'/Users/a b/bin/aboard' hook claude-code stop", "claude-code", "stop") {
		t.Fatal("a quoted path isn't recognized as Aboard's hook")
	}
}

// initEnv is a fresh home directory with Claude Code and Codex installed, and a project
// directory inside it to run in.
func initEnv(t *testing.T, home string) (Env, *strings.Builder, string) {
	t.Helper()
	project := filepath.Join(home, "project")
	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), project} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	out := &strings.Builder{}
	return Env{
		Stdin: strings.NewReader(""), Stdout: out, Stderr: out, Dir: project,
		Getenv:     func(k string) string { return map[string]string{"HOME": home}[k] },
		Executable: func() (string, error) { return exe, nil },
	}, out, project
}

// Doctor compares the files of the scope they are installed in: a project's outdated
// skill and hooks are reported, with the project fix. Nothing here reports Claude Code's
// version, so init wrote the hooks the oldest version Aboard works with runs.
func TestDoctorFlagsAnOutdatedProjectSetup(t *testing.T) {
	home := t.TempDir()
	env, out, project := initEnv(t, home)
	if code := Run(context.Background(), []string{"init", "--yes", "--scope", "project"}, env); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	a := &app{env: env}
	claude := harnessNamed(t, "claude-code")
	scopes, _, err := a.installedScopes(claude, hooksOf(t, "claude-code", "aboard"))
	if err != nil || !slices.Equal(scopes, []string{scopeProject}) {
		t.Fatalf("scopes %v, %v", scopes, err)
	}
	if c := a.checkSkill(claude); len(c) != 1 || c[0].Level != levelOK {
		t.Fatalf("current skill: %+v", c)
	}
	if c := a.checkHooksCurrent("claude_hooks", claude, scopes, claude.Hooks(a.hookExe(), ""), okCheck("claude_hooks", "ok")); c.Level != levelOK {
		t.Fatalf("current hooks: %+v", c)
	}

	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "aboard", "SKILL.md"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := &app{env: env}
	moved.env.Executable = func() (string, error) { return "/elsewhere/aboard", nil }
	fix := "run aboard init --yes --scope project in this project"
	// The install manifest shows the skill changed after init wrote it.
	if c := a.checkSkill(claude); len(c) != 1 || deref(c[0].Code) != "skill_edited" || !strings.HasPrefix(deref(c[0].Fix), fix) {
		t.Fatalf("edited skill: %+v", c)
	}
	if c := moved.checkHooksCurrent("claude_hooks", claude, scopes, claude.Hooks(moved.hookExe(), ""), okCheck("claude_hooks", "ok")); deref(c.Code) != "hooks_outdated" || deref(c.Fix) != fix {
		t.Fatalf("outdated hooks: %+v", c)
	}
}

// Moving a hook to another event takes Aboard's old entry out and keeps everyone else's.
func TestMergeHooksRemovesAboardsStaleEntries(t *testing.T) {
	old := `{"hooks": {
  "PostToolUse": [
    {"hooks": [{"type": "command", "command": "/usr/local/bin/aboard hook codex tool"}, {"type": "command", "command": "fmt-on-save"}]},
    {"hooks": [{"type": "command", "command": "/usr/local/bin/aboard hook codex tool"}]}
  ]
}}`
	out, changed, err := mergeHooks([]byte(old), "codex", hooksOf(t, "codex", exe))
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []hookEntry `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	post := parsed.Hooks["PostToolUse"]
	if len(post) != 1 || len(post[0].Hooks) != 1 || post[0].Hooks[0].Command != "fmt-on-save" {
		t.Fatalf("PostToolUse after the merge: %+v", post)
	}
	if pre := parsed.Hooks["PreToolUse"]; len(pre) != 1 || !isAboardHook(pre[0].Hooks[0].Command, "codex", "tool") {
		t.Fatalf("PreToolUse after the merge: %+v", pre)
	}
	old = `{"hooks": {"PostToolUse": [{"hooks": [{"type": "command", "command": "/usr/local/bin/aboard hook claude-code tool"}]}]}}`
	out, _, err = mergeHooks([]byte(old), "claude-code", hooksOf(t, "claude-code", exe))
	if err != nil || strings.Contains(string(out), `"PostToolUse"`) {
		t.Fatalf("an event left empty should go: %v\n%s", err, out)
	}
}
