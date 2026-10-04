package launchertest_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/launcher/launchertest"
)

// The herdr launcher shipped in launchers/herdr passes the kit through the launcher
// protocol, against a stand-in for herdr (e2e/fakeherdr) that serves herdr's socket API
// and runs each pane's command, so the test needs no herdr. The live suite runs the same
// kit against the real herdr (e2e/live, TestHerdrLauncherPassesTheKit). Both run with a
// config folder of the test's own, so herdr's sessions there are never the person's.
func TestHerdrLauncherPassesTheKitAgainstAStandIn(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	build(t, filepath.Join(bin, "aboard-launcher-herdr"), "./launchers/herdr")
	build(t, filepath.Join(bin, "herdr"), "./e2e/fakeherdr")
	config, err := os.MkdirTemp("/tmp", "aboard-herdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(config) })
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "XDG_CONFIG_HOME=") || strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, "HERDR_")
	})
	env = append(env, "XDG_CONFIG_HOME="+config, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	launchertest.RunCommand(t, "herdr", filepath.Join(bin, "aboard-launcher-herdr"), env)
	if entries, _ := os.ReadDir(filepath.Join(config, "herdr", "sessions")); len(entries) != 0 {
		t.Errorf("the launcher left herdr sessions behind once every agent stopped: %v", entries)
	}
}

// build builds a program of this module into out.
func build(t *testing.T, out, pkg string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", out, pkg) //nolint:gosec // builds this module's own packages
	cmd.Dir = "../../../.."
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, raw)
	}
}
