//go:build live

package live

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The terminal can show its prompt between Codex steps. Hook state, scoped to the
// pane's project, must keep that unfinished turn busy even while the screen looks idle.
func TestCodexIdleWaitsForThisPanesStopHook(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf '%s\\n' '? for shortcuts'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, events string
		submitted    bool
		stops        int
		want         bool
	}{
		{name: "submitted before prompt hook", submitted: true},
		{name: "this pane prompt has no stop", events: "prompt at=%s\n"},
		{name: "another panes stop cannot end this turn", events: "prompt at=%s\nstop at=another-project\n"},
		{name: "old stop cannot end submitted turn", events: "prompt at=%s\nstop at=%s\n", submitted: true, stops: 1},
		{name: "this panes stop ends the turn", events: "prompt at=%s\nstop at=%s\n", submitted: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &lab{t: t, dir: t.TempDir()}
			p := &pane{l: l, dir: t.TempDir(), harness: "codex", codexSubmitted: tc.submitted, codexStopsAtSubmit: tc.stops}
			tag := codexHookTag(p.dir)
			events := tc.events
			if events != "" {
				if tc.want || tc.stops > 0 {
					events = fmt.Sprintf(events, tag, tag)
				} else {
					events = fmt.Sprintf(events, tag)
				}
			}
			if err := os.WriteFile(l.codexHookLog(), []byte(events), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := codexIdle(p); got != tc.want {
				t.Fatalf("idle-looking screen with unfinished per-pane turn: idle=%v, want %v", got, tc.want)
			}
		})
	}
}
