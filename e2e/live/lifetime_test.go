//go:build live

package live

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperVar makes TestHelperStartsALab run: it is set only when
// TestLabStopsWhenTheTestThatStartedItIsKilled runs this test binary as its helper.
const helperVar = "ABOARD_LIVE_LIFETIME_HELPER"

// labProcesses is what the helper's lab started, by process id.
type labProcesses struct {
	Server int `json:"server"`
	Daemon int `json:"daemon"`
	Tmux   int `json:"tmux"`
	Pane   int `json:"pane"`
	// Temp is the helper's temporary directory, which holds the lab.
	Temp string `json:"temp"`
}

// A live test can die before its cleanups run: interrupted, timed out, or killed by
// whatever runs it. Its tmux server, the processes in its panes, and its local server
// and delivery daemon must stop anyway. This spends no model turns: the pane runs a
// shell command, not a harness.
func TestLabStopsWhenTheTestThatStartedItIsKilled(t *testing.T) {
	helper := command(t.Context(), os.Args[0], "-test.run=^TestHelperStartsALab$", "-test.count=1")
	helper.Env = append(os.Environ(), builtVar+"="+filepath.Dir(newBinary), helperVar+"=1")
	out, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The helper waits for its standard input to close, which it never does here.
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var stderr strings.Builder
	helper.Stderr = &stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); _ = helper.Wait() })
	var got labProcesses
	lines := bufio.NewScanner(out)
	for lines.Scan() {
		if v, ok := strings.CutPrefix(lines.Text(), "started "); ok {
			if err := json.Unmarshal([]byte(v), &got); err != nil {
				t.Fatalf("helper's line %q: %v", lines.Text(), err)
			}
			break
		}
	}
	procs := map[string]int{"the local server": got.Server, "the delivery daemon": got.Daemon, "the tmux server": got.Tmux, "the pane's process": got.Pane}
	for name, pid := range procs {
		if pid == 0 {
			t.Fatalf("the helper didn't say the process id of %s: %+v\n%s", name, got, stderr.String())
		}
	}
	go func() { _, _ = io.Copy(io.Discard, out) }()

	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()

	// Should they outlive the check, they are stopped here, not left running. The killed
	// helper couldn't remove its temporary directory either.
	t.Cleanup(func() {
		for _, pid := range procs {
			if syscall.Kill(pid, 0) == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if strings.Contains(filepath.Base(got.Temp), "TestHelperStartsALab") {
			_ = os.RemoveAll(got.Temp)
		}
	})
	for name, pid := range procs {
		if !waitQuietly(15*time.Second, func() bool { return syscall.Kill(pid, 0) != nil }) {
			t.Fatalf("%s (pid %d) still runs 15s after the test that started it was killed", name, pid)
		}
	}
}

// TestHelperStartsALab is the helper of TestLabStopsWhenTheTestThatStartedItIsKilled:
// it makes a lab with a local server, a delivery daemon and a tmux pane, prints their
// process ids, and waits to be killed.
func TestHelperStartsALab(t *testing.T) {
	if os.Getenv(helperVar) == "" {
		t.Skip("runs only as the helper of TestLabStopsWhenTheTestThatStartedItIsKilled")
	}
	l := newLab(t)
	l.run("up")
	l.run("daemon", "start")
	l.tmuxRun("new-session", "-d", "-s", "live", "-c", l.dir, "exec cat")
	pid := func(format string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(l.tmuxRun("display-message", "-p", "-t", "live:", format)))
		return n
	}
	line, err := json.Marshal(labProcesses{
		Server: pidFile(filepath.Join(l.dataDir(), "server.pid")),
		Daemon: pidFile(filepath.Join(l.stateDir(), "daemon.pid")),
		Tmux:   pid("#{pid}"),
		Pane:   pid("#{pane_pid}"),
		Temp:   filepath.Dir(l.dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("started " + string(line) + "\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// pidFile reads a process id from a pid file, or 0.
func pidFile(path string) int {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return n
}
