//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/install-hooks installs a pre-push hook that runs make quick, removes only its
// own hook, and leaves a pre-push hook someone else wrote alone.
func TestInstallHooksAddsAndRemovesThePrePushHook(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + dir,
		"PATH=" + systemPath,
		"GIT_CONFIG_NOSYSTEM=1",
	}
	repo := filepath.Join(dir, "repo")
	script, err := filepath.Abs(filepath.Join("..", "scripts", "install-hooks"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run("git", "init", "-q"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-push")

	if out, err := run(script); err != nil {
		t.Fatalf("install-hooks: %v\n%s", err, out)
	}
	b, err := os.ReadFile(hook)
	if err != nil {
		t.Fatalf("no hook: %v", err)
	}
	if !strings.Contains(string(b), "make quick") {
		t.Errorf("the hook doesn't run make quick:\n%s", b)
	}
	if out, err := run(script); err != nil {
		t.Errorf("installing again: %v\n%s", err, out)
	}
	if out, err := run(script, "--remove"); err != nil || !strings.Contains(out, "Removed") {
		t.Errorf("--remove: %v\n%s", err, out)
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Errorf("the hook is still there: %v", err)
	}

	mine := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(hook, []byte(mine), 0o755); err != nil { //nolint:gosec // a hook git runs
		t.Fatal(err)
	}
	if out, err := run(script); err == nil || !strings.Contains(out, "isn't one this script wrote") {
		t.Errorf("replaced someone else's hook: %v\n%s", err, out)
	}
	if _, err := run(script, "--remove"); err != nil {
		t.Errorf("--remove with someone else's hook: %v", err)
	}
	if b, _ := os.ReadFile(hook); string(b) != mine {
		t.Errorf("someone else's hook changed to:\n%s", b)
	}
}
