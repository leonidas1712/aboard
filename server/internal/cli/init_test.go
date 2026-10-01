package cli

import (
	"encoding/json"
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
	out, changed, err := mergeHooks([]byte(settings), "claude-code", claudeHooks(exe))
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

	again, changed, err := mergeHooks(out, "claude-code", claudeHooks(exe))
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
	if _, _, err := mergeHooks([]byte(`["not", "settings"]`), "claude-code", claudeHooks(exe)); err == nil {
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
