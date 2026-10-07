//go:build live

package live

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	killedTaskPID   = ".killtest-sleep.pid"
	killedTaskSleep = ".killtest-sleep"
)

// This proof's child has a project-specific executable name. Neither readiness nor
// cleanup can match a sleep belonging to another project or live-test process.
func writeKilledTask(t *testing.T, project string, seconds int) {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sleep, filepath.Join(project, killedTaskSleep)); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\n%s %d &\nchild=$!\nprintf '%%s\\n' \"$child\" > %s\nwait \"$child\"\necho finished\n", quote(filepath.Join(project, killedTaskSleep)), seconds, quote(filepath.Join(project, killedTaskPID)))
	if err := os.WriteFile(filepath.Join(project, slowTask), []byte(script), 0o700); err != nil { //nolint:gosec // the owned task must be executable
		t.Fatal(err)
	}
}

func killedTaskProcess(project string, seconds int) *os.Process {
	raw, err := os.ReadFile(filepath.Join(project, killedTaskPID)) //nolint:gosec // reads only the test project marker
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return nil
	}
	out, err := command(context.Background(), "ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil || strings.TrimSpace(string(out)) != fmt.Sprintf("%s %d", filepath.Join(project, killedTaskSleep), seconds) {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return p
}

func killedTaskReady(project string, seconds int) bool {
	return killedTaskProcess(project, seconds) != nil
}

func stopKilledTask(project string, seconds int) error {
	p := killedTaskProcess(project, seconds)
	if p == nil {
		return nil
	}
	return p.Kill()
}

func TestKilledTaskIgnoresUnrelatedSleep(t *testing.T) {
	unrelated := command(t.Context(), "sleep", "30")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
	project := t.TempDir()
	writeKilledTask(t, project, 30)
	if killedTaskReady(project, 30) {
		t.Fatal("unrelated sleep satisfied this project's readiness")
	}
	// A stale or wrong PID must not authorize killing a process with another argv.
	if err := os.WriteFile(filepath.Join(project, killedTaskPID), []byte(strconv.Itoa(unrelated.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if killedTaskReady(project, 30) {
		t.Fatal("unrelated PID satisfied this project's readiness")
	}
	if err := stopKilledTask(project, 30); err != nil {
		t.Fatal(err)
	}
	if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("cleanup targeted unrelated sleep", err)
	}
}

func TestKilledTaskOwnProcess(t *testing.T) {
	project := t.TempDir()
	writeKilledTask(t, project, 30)
	cmd := command(t.Context(), filepath.Join(project, slowTask))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stopKilledTask(project, 30); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	// Readiness is polled by the same bounded helper used in the live proof.
	l := &lab{t: t}
	l.waitFor(5*time.Second, "the owned child", func() bool { return killedTaskReady(project, 30) })
	if err := stopKilledTask(project, 30); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if killedTaskReady(project, 30) {
		t.Fatal("completed invocation remained ready")
	}
}
