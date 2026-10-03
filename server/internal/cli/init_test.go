package cli

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const exe = "/usr/local/bin/aboard"

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
	out, changed, err := mergeHooks([]byte(settings), "claude-code", claudeHooks(exe, true))
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
			Hooks []hookHandler `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if n := len(parsed.Hooks["Stop"]); n != 2 {
		t.Fatalf("Stop groups %d, want the existing one and Aboard's", n)
	}
	stop := parsed.Hooks["Stop"][1].Hooks[0]
	if !stop.AsyncRewake || stop.Timeout != stopHookTimeout {
		t.Fatalf("stop hook %+v", stop)
	}

	again, changed, err := mergeHooks(out, "claude-code", claudeHooks(exe, true))
	if err != nil || changed || string(again) != text {
		t.Fatalf("a second merge changed the file (changed %v, %v)", changed, err)
	}
}

func TestMergeHooksUpdatesAMovedBinaryInPlace(t *testing.T) {
	first, _, err := mergeHooks(nil, "codex", codexHooks("/old/place/aboard"))
	if err != nil {
		t.Fatal(err)
	}
	moved, changed, err := mergeHooks(first, "codex", codexHooks(exe))
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
	if _, _, err := mergeHooks([]byte(`["not", "settings"]`), "claude-code", claudeHooks(exe, true)); err == nil {
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

// initEnv is a terminal in a fresh home directory with Claude Code and Codex installed,
// in a project directory inside it, answering the questions with answers.
func initEnv(t *testing.T, home, answers string) (Env, *strings.Builder, string) {
	t.Helper()
	project := filepath.Join(home, "project")
	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), project} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	out := &strings.Builder{}
	return Env{
		Stdin: strings.NewReader(answers), Stdout: out, Stderr: out, Dir: project, Terminal: true,
		Getenv:     func(k string) string { return map[string]string{"HOME": home}[k] },
		Executable: func() (string, error) { return exe, nil },
	}, out, project
}

// files lists every file under dir, relative to it.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	return got
}

// In a terminal, aboard init asks its questions, shows the changes and writes them only
// once the person agrees; a project setup writes only under the project.
func TestInitAsksThenWritesTheProjectSetup(t *testing.T) {
	home := t.TempDir()
	// Every harness, this project, the current delivery mode, allow commands, confirm.
	env, out, project := initEnv(t, home, "\nproject\n\ny\ny\n")
	if code := Run(context.Background(), []string{"init"}, env); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"Set up which harnesses? claude-code, codex [all] ",
		"Install everywhere, or only in this project (" + project + ")?",
		// With Codex among the harnesses, the question says why and suggests yes.
		"Codex's sandbox blocks network access; aboard needs to reach its local server.\n" +
			"Let agents run aboard commands without a permission prompt? [Y/n] ",
		"create    ~/project/.claude/settings.local.json (hooks)",
		"allow: Bash(aboard *)",
		"create    ~/project/.codex/rules/aboard.rules (permissions)",
		"Make these changes? [y/N] ",
		"Done. ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	want := []string{
		".agents/skills/aboard/SKILL.md", ".claude/settings.local.json", ".claude/skills/aboard/SKILL.md",
		".codex/hooks.json", ".codex/rules/aboard.rules",
	}
	if got := files(t, project); !slices.Equal(got, want) {
		t.Fatalf("project files %v, want %v", got, want)
	}
	if got := files(t, home); len(got) != len(want) {
		t.Fatalf("files outside the project: %v", got)
	}
	settings, err := os.ReadFile(filepath.Clean(filepath.Join(project, ".claude", "settings.local.json")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{exe + " hook claude-code stop", `"Bash(aboard *)"`} {
		if !strings.Contains(string(settings), want) {
			t.Fatalf("settings lack %s:\n%s", want, settings)
		}
	}

	// Given the same answers again, there is nothing to do.
	env, out, _ = initEnv(t, home, "\nproject\n\ny\n")
	if code := Run(context.Background(), []string{"init"}, env); code != exitOK || !strings.HasSuffix(out.String(), "Nothing to change.\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// Declining the confirmation writes nothing.
func TestInitWritesNothingWhenDeclined(t *testing.T) {
	home := t.TempDir()
	env, out, _ := initEnv(t, home, "\n\n\n\nn\n")
	if code := Run(context.Background(), []string{"init"}, env); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "create    ~/.claude/settings.json (hooks)") || !strings.HasSuffix(out.String(), "Nothing changed.\n") {
		t.Fatalf("output:\n%s", out)
	}
	if got := files(t, home); len(got) != 0 {
		t.Fatalf("declining wrote %v", got)
	}
}

// Doctor compares the files of the scope they are installed in: a project's outdated
// skill and hooks are reported, with the project fix.
func TestDoctorFlagsAnOutdatedProjectSetup(t *testing.T) {
	home := t.TempDir()
	env, out, project := initEnv(t, home, "")
	if code := Run(context.Background(), []string{"init", "--yes", "--scope", "project"}, env); code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	a := &app{env: env}
	scopes, _, err := a.installedScopes("claude-code", claudeHooks("aboard", true))
	if err != nil || !slices.Equal(scopes, []string{scopeProject}) {
		t.Fatalf("scopes %v, %v", scopes, err)
	}
	if c := a.checkSkill("claude_skill", "claude-code"); len(c) != 1 || c[0].Level != levelOK {
		t.Fatalf("current skill: %+v", c)
	}
	if c := a.checkHooksCurrent("claude_hooks", "claude-code", scopes, claudeHooks(a.hookExe(), true), okCheck("claude_hooks", "ok")); c.Level != levelOK {
		t.Fatalf("current hooks: %+v", c)
	}

	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "aboard", "SKILL.md"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := &app{env: env}
	moved.env.Executable = func() (string, error) { return "/elsewhere/aboard", nil }
	fix := "run aboard init --yes --scope project in this project"
	if c := a.checkSkill("claude_skill", "claude-code"); len(c) != 1 || deref(c[0].Code) != "skill_outdated" || deref(c[0].Fix) != fix {
		t.Fatalf("outdated skill: %+v", c)
	}
	if c := moved.checkHooksCurrent("claude_hooks", "claude-code", scopes, claudeHooks(moved.hookExe(), true), okCheck("claude_hooks", "ok")); deref(c.Code) != "hooks_outdated" || deref(c.Fix) != fix {
		t.Fatalf("outdated hooks: %+v", c)
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"2.1.288 (Claude Code)", true},
		{"2.1.118 (Claude Code)", true},
		{"2.1.117 (Claude Code)", false},
		{"2.0.999", false},
		{"3.0", true},
		{"1.9.200 (Claude Code)", false},
		{"", true},
		{"not a version", true},
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.text, claudeBatchSince); got != tt.want {
			t.Errorf("versionAtLeast(%q, %s) = %v, want %v", tt.text, claudeBatchSince, got, tt.want)
		}
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
	out, changed, err := mergeHooks([]byte(old), "codex", codexHooks(exe))
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	var parsed struct {
		Hooks map[string][]struct {
			Hooks []hookHandler `json:"hooks"`
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
	out, _, err = mergeHooks([]byte(old), "claude-code", claudeHooks(exe, true))
	if err != nil || strings.Contains(string(out), `"PostToolUse"`) {
		t.Fatalf("an event left empty should go: %v\n%s", err, out)
	}
}
