//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexDoctorObservesHookApproval(t *testing.T) {
	only(t, "codex")
	setup := requireCodex(t)
	parallel(t)
	l := newLab(t)
	home := l.codexHome(setup)
	l.run("init", "--yes", "--scope", "global", "--harness", "codex", "--allow-commands")
	assertReview := func(want bool) {
		t.Helper()
		before := readFile(t, filepath.Join(home, "config.toml"))
		seen := false
		for _, check := range l.doctor(l.human) {
			if check.Code != nil && *check.Code == "codex_hooks_need_review" {
				seen = true
			}
			if check.Code != nil && *check.Code == "codex_hooks_review_unavailable" {
				t.Fatalf("Codex did not expose approval state: %+v", check)
			}
		}
		if seen != want {
			t.Fatalf("doctor pending approval: %v, want %v", seen, want)
		}
		if !bytes.Equal(before, readFile(t, filepath.Join(home, "config.toml"))) {
			t.Fatal("doctor changed Codex approval configuration")
		}
	}
	assertReview(true)
	l.trustCodexHooks(l.human, home, l.vars, "user", "global")
	assertReview(false)
	path := filepath.Join(home, "hooks.json")
	var file struct {
		Hooks map[string][]struct {
			Matcher *string                      `json:"matcher,omitempty"`
			Hooks   []map[string]json.RawMessage `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(readFile(t, path), &file); err != nil {
		t.Fatal(err)
	}
	stop := file.Hooks["Stop"][0].Hooks[0]
	stop["timeout"] = json.RawMessage("61")
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	assertReview(true)
	l.trustCodexHooks(l.human, home, l.vars, "user", "global")
	assertReview(false)
}
