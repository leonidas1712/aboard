package codex

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/harness"
)

func TestCodexStopAndToolHooksFollowInstalledVersion(t *testing.T) {
	h := New()
	for _, tt := range []struct{ version, stop, tool string }{
		{"0.159.3", "stop", "PreToolUse"},
		{"0.160.0", "stop-continue", "PostToolUse"},
		{"", "stop", "PreToolUse"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			stops, tools := 0, 0
			for _, hook := range h.Hooks("aboard", tt.version) {
				if hook.Event == "Stop" {
					stops++
					if hook.Arg != tt.stop {
						t.Errorf("Stop runs %s, want %s", hook.Arg, tt.stop)
					}
				}
				if hook.Event == "PreToolUse" || hook.Event == "PostToolUse" {
					tools++
					if hook.Event != tt.tool {
						t.Errorf("tool event %s, want %s", hook.Event, tt.tool)
					}
				}
			}
			if stops != 1 || tools != 1 {
				t.Fatalf("want one Stop and one tool hook, got %d/%d", stops, tools)
			}
		})
	}
	if call, ok := h.HookCall("stop-continue", harness.HookInput{}); !ok || call.Op != harness.OpWait {
		t.Fatalf("no Stop continuation wait operation: %+v/%v", call, ok)
	}
}
