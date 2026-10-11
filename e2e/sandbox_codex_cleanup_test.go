//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSandboxCleanupStopsOnlyItsPrivateCodexProcesses(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sleeper.go")
	if err := os.WriteFile(source, []byte("package main\nimport \"time\"\nfunc main(){time.Sleep(time.Minute)}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sleeper := filepath.Join(t.TempDir(), "sleeper")
	if out, err := exec.Command("go", "build", "-o", sleeper, source).CombinedOutput(); err != nil {
		t.Fatalf("build sleeper: %v %s", err, out)
	}
	for _, action := range []string{"clean", "team-stop", "team-clean"} {
		t.Run(action, func(t *testing.T) {
			home := t.TempDir()
			name := "maya"
			dir := filepath.Join(home, ".aboard-sandboxes", name)
			if action != "clean" {
				name = "qa"
				dir = filepath.Join(home, ".aboard-sandboxes", "teams", name, "people", "maya")
			}
			owned := cleanupSleeper(t, sleeper, filepath.Join(dir, "codex", "packages", "codex"))
			outside := cleanupSleeper(t, sleeper, filepath.Join(home, "real-codex", "packages", "codex"))
			helper := filepath.Join(home, "team-helper")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			sandboxScript(t, home, []string{"SANDBOX_TEAM_HELPER=" + helper}, action, name)
			select {
			case <-owned:
			case <-time.After(4 * time.Second):
				t.Fatal("private Codex daemon survived cleanup")
			}
			select {
			case <-outside:
				t.Fatal("cleanup stopped another Codex installation")
			default:
			}
		})
	}
}

func TestSandboxCodexCleanupRefusesARedirectedHome(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "other-codex")
	if err := os.MkdirAll(filepath.Join(real, "packages"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{filepath.Join(home, "codex"), filepath.Join(home, "person")} {
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		target := link
		if filepath.Base(link) == "person" {
			if err := os.MkdirAll(filepath.Join(real, "codex", "packages"), 0o700); err != nil {
				t.Fatal(err)
			}
			target = filepath.Join(link, "codex")
		}
		cmd := exec.Command("python3", "../scripts/stop-sandbox-codex.py", target)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("redirected home accepted: %s", out)
		}
	}
}

func cleanupSleeper(t *testing.T, source, path string) <-chan error {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return done
}
