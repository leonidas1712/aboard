package tmux_test

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/launcher/launchertest"
	"github.com/leonidas1712/aboard/server/internal/launcher/tmux"
)

// The tmux launcher passes the launcher kit. Its tmux servers live in a socket folder of
// the test's own (TMUX_TMPDIR), never beside the person's.
func TestTmuxLauncherPassesTheKit(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux isn't installed, and the tmux launcher's kit needs it. Install it: brew install tmux, or apt install tmux.")
	}
	sockets, err := os.MkdirTemp("/tmp", "aboard-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	// No locale either: tmux then writes some characters of its output differently.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "LANG=") || strings.HasPrefix(kv, "LC_")
	})
	env = append(env, "TMUX_TMPDIR="+sockets)
	launchertest.Run(t, tmux.Launcher{Env: env})
}
