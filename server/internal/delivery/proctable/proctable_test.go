package proctable_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/proctable"
)

// TestMain lets the test binary play a harness that runs a child: with
// PROCTABLE_REPORT set, it prints the harness the child process finds.
func TestMain(m *testing.M) {
	if os.Getenv("PROCTABLE_REPORT") != "" {
		p, ok := proctable.Harness()
		if !ok {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString(strconv.Itoa(p.PID))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A command run through a shell finds the program that ran the shell, not the shell.
func TestHarnessSkipsShells(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", `"$0"; true`, self) //nolint:gosec // self is this test binary
	cmd.Env = append(os.Environ(), "PROCTABLE_REPORT=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	if got, want := string(out), strconv.Itoa(os.Getpid()); got != want {
		t.Fatalf("harness pid %s, want this test process %s", got, want)
	}
}

func TestAliveUntilTheProcessExits(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p, ok := proctable.Lookup(cmd.Process.Pid)
	if !ok {
		t.Fatalf("process %d not found", cmd.Process.Pid)
	}
	tab := proctable.Table{}
	if !tab.Alive(p) {
		t.Fatal("a running process reads as dead")
	}
	if tab.Alive(delivery.Process{PID: p.PID, Start: p.Start + 1}) {
		t.Fatal("a process with another start time reads as the same one")
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if tab.Alive(p) {
		t.Fatal("an exited process reads as alive")
	}
}
