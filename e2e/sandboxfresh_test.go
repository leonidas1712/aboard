//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshTeamSandboxInstallsTheLocalBuild(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "aboard-fresh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	helper := filepath.Join(home, "team-helper")
	if out, err := exec.Command("go", "build", "-o", helper, "../scripts/sandboxteam").CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	installed := filepath.Join(home, "installed-tools")
	if err := os.Mkdir(installed, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"aboard": binary, "claude": filepath.Join(fakeBin, "claude")} {
		if err := os.Symlink(source, filepath.Join(installed, name)); err != nil {
			t.Fatal(err)
		}
	}
	extra := []string{"SANDBOX_TEAM_HELPER=" + helper, "PATH=" + installed + string(os.PathListSeparator) + fakeBin + string(os.PathListSeparator) + systemPath}
	t.Cleanup(func() { _ = sandboxCmd(home, extra, "team-clean", "qa").Run() })
	sandboxScript(t, home, extra, "team-start", "qa")
	cmd := `if command -v aboard >/dev/null; then echo 'aboard already present'; exit 42; fi
 test -z "$(ls -A .)" || exit 43
 printf 'project=%s\n' "$PWD"
 curl -fsSL https://comeaboard.dev/install | sh
 aboard version
 aboard skill >/dev/null
 test -x "$(command -v claude)"
 printf 'installed=%s\n' "$(command -v aboard)"`
	out := sandboxScript(t, home, append(extra, "TEAM=qa", "FRESH=1", "SANDBOX_CMD="+cmd), "open", "maya")
	dir := filepath.Join(home, ".aboard-sandboxes", "teams", "qa", "people", "maya")
	for _, want := range []string{"project=" + filepath.Join(dir, "project"), "installed=" + filepath.Join(dir, "home", ".local", "bin", "aboard"), "local development build"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "aboard")); !os.IsNotExist(err) {
		t.Fatalf("installer wrote original HOME: %v", err)
	}
}

func TestInstallDevSourceRequiresSandboxAndPrivateDestination(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		want  string
	}{
		{"not sandbox", nil, "only for a named dev sandbox"},
		{"outside home", []string{"ABOARD_SANDBOX_NAME=qa", "ABOARD_INSTALL_DIR=/tmp/outside-aboard-test"}, "only in the sandbox HOME"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cmd := exec.Command("sh", "../scripts/install.sh")
			cmd.Env = append([]string{"HOME=" + home, "PATH=" + systemPath, "ABOARD_INSTALL_FROM=" + binary}, tc.extra...)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("guard: %v %s", err, out)
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", "aboard")); !os.IsNotExist(err) {
				t.Fatalf("guard installed a binary: %v", err)
			}
		})
	}
}
