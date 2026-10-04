//go:build launcherkit

package launchertest_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/launcher/headless"
	"github.com/leonidas1712/aboard/server/internal/launcher/launchertest"
	"github.com/leonidas1712/aboard/server/internal/launcher/tmux"
)

// TestLauncherKit runs the kit against the launcher LAUNCHER names: a built-in one, or
// the aboard-launcher-<name> on the PATH. make launcher-kit LAUNCHER=<name> runs it.
func TestLauncherKit(t *testing.T) {
	name := os.Getenv("LAUNCHER")
	switch name {
	case "":
		t.Fatal("Name the launcher to check: make launcher-kit LAUNCHER=<name>, such as herdr for aboard-launcher-herdr on your PATH.")
	case tmux.Name:
		launchertest.Run(t, tmux.Launcher{})
	case headless.Name:
		launchertest.Run(t, headless.Launcher{Dir: t.TempDir()})
	default:
		path, err := exec.LookPath("aboard-launcher-" + name)
		if err != nil {
			t.Fatalf("aboard-launcher-%s isn't on the PATH: %v", name, err)
		}
		launchertest.RunCommand(t, name, path, nil)
	}
}
