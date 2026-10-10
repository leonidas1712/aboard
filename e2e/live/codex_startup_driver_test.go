//go:build live

package live

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexPromptTakenDistinguishesStartupFromEditableInput(t *testing.T) {
	const prefix = "Run aboard resume writer"
	for _, tc := range []struct {
		name, screen string
		want         bool
	}{
		{"submitted while startup waits", "› Run aboard resume writer\n\n  Waiting for startup  · esc cancel\n? for shortcuts", true},
		{"editable below startup", "  Waiting for startup  · esc cancel\n› Run aboard resume writer\n? for shortcuts", false},
		{"still editable", "› Run aboard resume writer\n? for shortcuts", false},
		{"finished history with empty composer", "› Run aboard resume writer\n• OK\n›\n? for shortcuts", true},
		{"startup without submitted prompt", "  Waiting for startup  · esc cancel\n› Run aboard resume writer\n? for shortcuts", false},
		{"startup mentioned in prompt", "› Run aboard resume writer and print Waiting for startup · esc cancel\n? for shortcuts", false},
		{"startup without cancellation control", "› Run aboard resume writer\n• Waiting for startup\n? for shortcuts", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexPromptTaken(tc.screen, prefix); got != tc.want {
				t.Fatalf("taken=%v, want %v: %s", got, tc.want, tc.screen)
			}
		})
	}
}

func TestCodexStartupIsBusyEvenWithoutAnOpenHookTurn(t *testing.T) {
	p, dir := terminalDriverFixture(t, `shift 2
case "$1" in
 capture-pane) cat "$TERMINAL_DRIVER_STATE/screen" ;;
esac
`)
	p.dir = dir
	for _, events := range []string{"", "stop-complete at=" + codexHookTag(dir) + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, "screen"), []byte("› Run aboard resume writer\n  Waiting for startup  · esc cancel\n? for shortcuts\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.l.codexHookLog(), []byte(events), 0o600); err != nil {
			t.Fatal(err)
		}
		if codexIdle(p) {
			t.Fatal("startup was treated as idle")
		}
	}
}
