//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The install script and aboard upgrade write each program next to the one it replaces
// under a temporary name. A link planted at a name someone could guess beforehand (the
// installer's process id) must not make them write through it.

// plantedVictim is a file outside the install folder and the link to it planted in the
// folder at name, where $$ becomes the process id of the shell that then execs the
// installer. It returns the shell line that plants the link and the victim's path.
func plantedVictim(t *testing.T, home, dir, name string) (plant, victim string) {
	t.Helper()
	victim = filepath.Join(home, "victim")
	if err := os.WriteFile(victim, []byte("victim\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return "/bin/ln -s '" + victim + "' '" + dir + "'/" + name + " && exec ", victim
}

func TestInstallScriptDoesNotWriteThroughAPlantedLink(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.existing()
	plant, victim := plantedVictim(t, r.home, r.installed(), `".aboard.install.$$"`)
	cmd := exec.Command(filepath.Join(r.bin, "sh"), "-c", plant+"sh "+filepath.Join("..", "scripts", "install.sh"))
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if got := readFile(t, victim); got != "victim\n" {
		t.Fatalf("the script wrote through the planted link (%d bytes now)", len(got))
	}
	exe := filepath.Join(r.installed(), "aboard")
	if info, err := os.Lstat(exe); err != nil || !info.Mode().IsRegular() || readFile(t, exe) != fakeAboard {
		t.Fatalf("installed aboard: %v %v", info, err)
	}
}

func TestUpgradeDoesNotWriteThroughAPlantedLink(t *testing.T) {
	t.Parallel()
	u := newUpgradeEnv(t)
	plant, victim := plantedVictim(t, u.home, filepath.Dir(u.exe), `".aboard.upgrade.$$"`)
	cmd := exec.Command("/bin/sh", "-c", plant+"'"+u.exe+"' upgrade")
	cmd.Dir = u.dir
	cmd.Env = u.vars
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if got := readFile(t, victim); got != "victim\n" {
		t.Fatalf("aboard upgrade wrote through the planted link (%d bytes now)", len(got))
	}
	if info, err := os.Lstat(u.exe); err != nil || !info.Mode().IsRegular() || readFile(t, u.exe) != readFile(t, binary) {
		t.Fatalf("upgraded aboard: %v %v", info, err)
	}
}
