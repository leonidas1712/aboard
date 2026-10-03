package harness

import (
	"encoding/json"
	"testing"
)

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
		if got := VersionAtLeast(tt.text, "2.1.118"); got != tt.want {
			t.Errorf("VersionAtLeast(%q, 2.1.118) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// A hook entry is written type, command, timeout, then the options in key order, the
// same every time: a harness asks the person to trust its hooks again when an entry's
// text changes.
func TestHookEntriesAreWrittenTheSameWayEveryTime(t *testing.T) {
	h := Handler{Type: "command", Command: "aboard hook x stop", Timeout: 30, Options: map[string]any{"zeta": 1, "asyncRewake": true}}
	for range 20 {
		raw, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"type":"command","command":"aboard hook x stop","timeout":30,"asyncRewake":true,"zeta":1}`; string(raw) != want {
			t.Fatalf("got %s, want %s", raw, want)
		}
	}
	// Like any value json.Marshal writes, with <, > and & escaped.
	raw, _ := json.Marshal(Handler{Type: "command", Command: "a <b>"})
	if want := "{\"type\":\"command\",\"command\":\"a \\u003cb\\u003e\"}"; string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
}

func TestShellWordQuotesOnlyWhenNeeded(t *testing.T) {
	tests := map[string]string{
		"/usr/local/bin/aboard": "/usr/local/bin/aboard",
		"/Users/a b/bin/aboard": "'/Users/a b/bin/aboard'",
		"/Users/it's/aboard":    `'/Users/it'\''s/aboard'`,
	}
	for in, want := range tests {
		if got := ShellWord(in); got != want {
			t.Errorf("ShellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
