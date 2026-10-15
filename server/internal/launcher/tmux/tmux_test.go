package tmux_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/launcher"
	"github.com/leonidas1712/aboard/server/internal/launcher/launchertest"
	"github.com/leonidas1712/aboard/server/internal/launcher/tmux"
)

// The tmux launcher passes the launcher kit. Its tmux servers live in a socket folder of
// the test's own (TMUX_TMPDIR), never beside the person's.
func TestTmuxLauncherPassesTheKit(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux isn't installed, and the tmux launcher's kit needs it. Install it: brew install tmux, or apt install tmux.")
	}
	sockets, err := os.MkdirTemp("/tmp", "aboard-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	// No locale either: tmux then writes some characters of its output differently.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "LANG=") || strings.HasPrefix(kv, "LC_")
	})
	env = append(env, "TMUX_TMPDIR="+sockets, "HOME="+sockets)
	launchertest.Run(t, tmux.Launcher{Env: env})
}

func TestReplacingADeadWindowDoesNotSignalItsReusedPanePID(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "aboard-tmux-dead-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "HOME=")
	})
	env = append(env, "TMUX_TMPDIR="+dir, "HOME="+dir)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	victim := exec.CommandContext(ctx, "/bin/cat")
	input, err := victim.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := victim.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = victim.Process.Kill(); _ = victim.Wait() })
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, "REAL_TMUX="+realTmux, "REUSED_PID="+strconv.Itoa(victim.Process.Pid))
	l := tmux.Launcher{Env: env}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		cmd := exec.CommandContext(cleanup, "tmux", "-L", "dead", "kill-server")
		cmd.Env = env
		_ = cmd.Run()
	})
	req := launcher.StartRequest{Swarm: "dead", Agent: "worker", Dir: dir, Mode: launcher.ModeInteractive, Argv: []string{"/bin/true"}}
	first, err := l.Start(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	ref := launcher.Ref{Swarm: req.Swarm, Agent: req.Agent, Handle: first.Handle}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		state, err := l.Status(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if state == launcher.Exited {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-tick.C:
		}
	}
	// Model PID reuse at the external tmux boundary, keeping the real window/server.
	shim := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nif [ \"$3\" = list-windows ]; then\n  \"$REAL_TMUX\" \"$@\" | awk -v pid=\"$REUSED_PID\" '{if ($4 == 1) $3 = pid; print}'\nelse\n  exec \"$REAL_TMUX\" \"$@\"\nfi\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil { //nolint:gosec // the owned tmux stand-in must be executable
		t.Fatal(err)
	}
	l.Command = shim
	req.Argv = []string{"/bin/cat"}
	_, startErr := l.Start(ctx, req)
	if _, err := io.WriteString(input, "still alive\n"); err != nil {
		t.Fatalf("replacement signaled an unrelated process: %v", err)
	}
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "still alive\n" {
		t.Fatalf("unrelated process did not survive replacement: %q, %v", line, err)
	}
	if startErr != nil {
		t.Fatal(startErr)
	}
}

// A pending tmux command keeps the empty server alive after its last window closes.
// Stop must observe that teardown, rather than reporting success from the pane PID.
func TestStopWaitsForTheLastTmuxServer(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "aboard-tmux-stop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_TMPDIR=") || strings.HasPrefix(kv, "HOME=")
	})
	env = append(env, "TMUX_TMPDIR="+dir, "HOME="+dir)
	l := tmux.Launcher{Env: env}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "tmux", append([]string{"-L", "stop"}, args...)...) //nolint:gosec // tmux commands and paths belong to this test
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v: %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	started, err := l.Start(t.Context(), launcher.StartRequest{
		Swarm: "stop", Agent: "worker", Dir: dir, Mode: launcher.ModeInteractive,
		Argv: []string{"/bin/sh", "-c", "exec cat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tmux", "-L", "stop", "kill-server")
		cmd.Env = env
		_ = cmd.Run()
	})
	serverPID, err := strconv.Atoi(run("display-message", "-p", "#{pid}"))
	if err != nil {
		t.Fatal(err)
	}
	gate, ready := filepath.Join(dir, "gate"), filepath.Join(dir, "ready")
	if err := syscall.Mkfifo(gate, 0o600); err != nil {
		t.Fatal(err)
	}
	job := exec.CommandContext(t.Context(), "tmux", "-L", "stop", "run-shell", "echo ready > "+ready+"; exec cat "+gate) //nolint:gosec // paths belong to this test's short temporary directory
	job.Env = env
	if err := job.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = job.Process.Kill()
		_ = job.Wait()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("tmux command did not start")
		case <-tick.C:
		}
	}
	stopCtx, stopCancel := context.WithTimeout(t.Context(), time.Second)
	defer stopCancel()
	state, err := l.Stop(stopCtx, launcher.Ref{Swarm: "stop", Agent: "worker", Handle: started.Handle})
	if !errors.Is(err, context.DeadlineExceeded) || state == launcher.Exited {
		t.Fatalf("stop reported %s, %v while the last server still has a pending job", state, err)
	}
	if err := syscall.Kill(serverPID, 0); err != nil {
		t.Fatalf("pending command did not hold the server: %v", err)
	}
	check := exec.CommandContext(t.Context(), "tmux", "-L", "stop", "has-session", "-t", "stop")
	check.Env = env
	if err := check.Run(); err == nil {
		t.Fatal("stop did not remove the last window's session before its deadline")
	}
}
