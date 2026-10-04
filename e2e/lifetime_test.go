//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperVar makes TestHelperStartsAMachine run: it is set only when
// TestProcessesStopWhenTheTestThatStartedThemIsKilled runs this test binary as its helper.
const helperVar = "ABOARD_E2E_LIFETIME_HELPER"

// started is what the helper started, by process id.
type started struct {
	Server  int `json:"server"`
	Daemon  int `json:"daemon"`
	Harness int `json:"harness"`
	// Temp is the helper's temporary directory, which holds its home.
	Temp string `json:"temp"`
}

// A test process can die before its cleanups run: interrupted, timed out, or killed by
// whatever runs it. The local server, the delivery daemon and the harness process it
// started must stop anyway, rather than run on with nothing left to stop them.
func TestProcessesStopWhenTheTestThatStartedThemIsKilled(t *testing.T) {
	t.Parallel()
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperStartsAMachine$", "-test.count=1") //nolint:gosec // this test binary
	helper.Env = append(os.Environ(), builtVar+"="+filepath.Dir(binary), helperVar+"=1")
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
	var got started
	lines := bufio.NewScanner(out)
	for lines.Scan() {
		if v, ok := strings.CutPrefix(lines.Text(), "started "); ok {
			if err := json.Unmarshal([]byte(v), &got); err != nil {
				t.Fatalf("helper's line %q: %v", lines.Text(), err)
			}
			break
		}
	}
	if got.Server == 0 || got.Daemon == 0 || got.Harness == 0 {
		t.Fatalf("the helper didn't say what it started: %+v\n%s", got, stderr.String())
	}
	go func() { _, _ = io.Copy(io.Discard, out) }()

	if err := helper.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()

	// Should they outlive the check, they are stopped here, not left running. The killed
	// helper couldn't remove its temporary directory either.
	t.Cleanup(func() {
		for _, pid := range []int{got.Server, got.Daemon, got.Harness} {
			if alive(pid) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if strings.Contains(filepath.Base(got.Temp), "TestHelperStartsAMachine") {
			_ = os.RemoveAll(got.Temp)
		}
	})
	for name, pid := range map[string]int{"the local server": got.Server, "the delivery daemon": got.Daemon, "the fake harness": got.Harness} {
		eventually(t, 10*time.Second, name+" to stop after the test that started it was killed", func() bool { return !alive(pid) })
	}
}

// TestHelperStartsAMachine is the helper of
// TestProcessesStopWhenTheTestThatStartedThemIsKilled: it starts a local server, a
// delivery daemon and a harness session the way any test does, prints their process
// ids, and waits to be killed.
func TestHelperStartsAMachine(t *testing.T) {
	if os.Getenv(helperVar) == "" {
		t.Skip("runs only as the helper of TestProcessesStopWhenTheTestThatStartedThemIsKilled")
	}
	e := newEnv(t)
	e.run("up")
	_, harness := e.claudeSessionIn("s1") // its session-start hook starts the daemon
	line, err := json.Marshal(started{
		Server:  pidIn(filepath.Join(e.dataDir(), "server.pid")),
		Daemon:  pidIn(filepath.Join(e.stateDir(), "daemon.pid")),
		Harness: harness.Process.Pid,
		Temp:    filepath.Dir(e.home),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("started " + string(line) + "\n"); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
