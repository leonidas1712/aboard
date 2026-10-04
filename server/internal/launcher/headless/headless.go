// Package headless is the built-in headless launcher: it starts a session's command as
// a background process with no terminal, in a process group of its own, its output in a
// log file. aboard swarm up gives it the headless runner (aboard swarm runner), which
// waits on the agent's inbox and runs one non-interactive turn of the harness per batch
// of messages.
package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/proctable"
	"github.com/leonidas1712/aboard/server/internal/launcher"
)

// Name is the launcher's name in the board file.
const Name = "headless"

// Launcher starts background processes.
type Launcher struct {
	// Dir holds each swarm's running processes and their logs: <Dir>/<swarm>/<agent>.json
	// and <agent>.log.
	Dir string
	// Env is the environment the processes start from; nil means this process's.
	Env []string
}

var _ launcher.Launcher = Launcher{}

// Info says the launcher starts headless sessions.
func (Launcher) Info(context.Context) (launcher.Info, error) {
	return launcher.Info{Name: Name, Modes: []string{launcher.ModeHeadless}}, nil
}

// record is what the launcher keeps about a process it started.
type record struct {
	PID   int   `json:"pid"`
	Start int64 `json:"start"`
}

// Start starts the command in the background.
func (l Launcher) Start(_ context.Context, req launcher.StartRequest) (launcher.Started, error) {
	switch {
	case req.Swarm == "" || req.Agent == "" || len(req.Argv) == 0 || req.Dir == "":
		return launcher.Started{}, launcher.Errorf("invalid_request", "Send swarm, agent, argv and dir.", "A start request needs a swarm, an agent, a command and a folder.")
	case req.Mode != launcher.ModeHeadless:
		return launcher.Started{}, launcher.Errorf("mode_unsupported", "Use the tmux launcher for interactive agents.", "The headless launcher starts only headless sessions, not %q.", req.Mode)
	case strings.ContainsAny(req.Swarm+req.Agent, `/\`) || strings.HasPrefix(req.Swarm, ".") || strings.HasPrefix(req.Agent, "."):
		return launcher.Started{}, launcher.Errorf("invalid_request", "Use names of letters, digits and dashes.", "%q and %q can't name files.", req.Swarm, req.Agent)
	}
	if rec, ok := l.read(req.Swarm, req.Agent); ok && alive(rec) {
		return launcher.Started{}, launcher.Errorf("already_running", "Stop it first with aboard swarm down "+req.Agent+".",
			"%s already runs in swarm %s, as process %d.", req.Agent, req.Swarm, rec.PID)
	}
	dir := filepath.Join(l.Dir, req.Swarm)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return launcher.Started{}, fmt.Errorf("make %s: %w", dir, err)
	}
	logPath := filepath.Join(dir, req.Agent+".log")
	logFile, err := os.OpenFile(filepath.Clean(logPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return launcher.Started{}, fmt.Errorf("open %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()
	env := l.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string{}, env...)
	for k, v := range req.Env {
		env = append(env, k+"="+v)
	}
	path, err := exec.LookPath(req.Argv[0])
	if err != nil {
		return launcher.Started{}, launcher.Errorf("start_failed", "Check that it is installed and on the PATH.", "%s isn't on the PATH.", req.Argv[0])
	}
	// The process must outlive this command, so it is tied to no context, and gets a
	// session of its own, so stop can end it and anything it started together.
	cmd := &exec.Cmd{
		Path: path, Args: req.Argv, Dir: req.Dir, Env: env, Stdout: logFile, Stderr: logFile,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return launcher.Started{}, launcher.Errorf("start_failed", "Check the command and the folder.", "Couldn't start %s: %v", req.Argv[0], err)
	}
	// Reap it when it ends while this process still runs, so it never lingers as a zombie
	// that looks alive.
	go func() { _ = cmd.Wait() }()
	rec := record{PID: cmd.Process.Pid}
	if start, ok := (proctable.Table{}).StartTime(rec.PID); ok {
		rec.Start = start
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return launcher.Started{}, fmt.Errorf("encode the process record: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, req.Agent+".json"), raw, 0o600); err != nil {
		return launcher.Started{}, fmt.Errorf("record the process: %w", err)
	}
	return launcher.Started{
		Handle: fmt.Sprintf("%d:%d", rec.PID, rec.Start), PID: rec.PID, Attach: "tail -f " + logPath,
	}, nil
}

// Status reports whether the process still runs.
func (l Launcher) Status(_ context.Context, ref launcher.Ref) (launcher.State, error) {
	rec, err := parse(ref.Handle)
	if err != nil {
		return "", err
	}
	if alive(rec) {
		return launcher.Running, nil
	}
	return launcher.Exited, nil
}

// Stop ends the process and everything in its process group: SIGTERM, then SIGKILL
// after 5 seconds.
func (l Launcher) Stop(ctx context.Context, ref launcher.Ref) (launcher.State, error) {
	rec, err := parse(ref.Handle)
	if err != nil {
		return "", err
	}
	if alive(rec) {
		_ = syscall.Kill(-rec.PID, syscall.SIGTERM)
		if !waitGone(ctx, rec, 5*time.Second) {
			_ = syscall.Kill(-rec.PID, syscall.SIGKILL)
			waitGone(ctx, rec, 5*time.Second)
		}
	}
	if cur, ok := l.read(ref.Swarm, ref.Agent); ok && cur.PID == rec.PID {
		if err := os.Remove(filepath.Join(l.Dir, ref.Swarm, ref.Agent+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("remove the process record: %w", err)
		}
	}
	return launcher.Exited, nil
}

func (l Launcher) read(swarm, agent string) (record, bool) {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(l.Dir, swarm, agent+".json")))
	if err != nil {
		return record{}, false
	}
	var rec record
	return rec, json.Unmarshal(raw, &rec) == nil && rec.PID > 0
}

func parse(handle string) (record, error) {
	pid, start, ok := strings.Cut(handle, ":")
	p, err1 := strconv.Atoi(pid)
	s, err2 := strconv.ParseInt(start, 10, 64)
	if !ok || err1 != nil || err2 != nil || p <= 0 {
		return record{}, launcher.Errorf("invalid_request", "Pass the handle start returned.", "%q is not a headless launcher handle.", handle)
	}
	return record{PID: p, Start: s}, nil
}

// alive reports whether the process runs, telling a reused process id apart by its
// start time when that was known.
func alive(rec record) bool {
	if rec.Start == 0 {
		return syscall.Kill(rec.PID, 0) == nil
	}
	return proctable.Table{}.Alive(delivery.Process{PID: rec.PID, Start: rec.Start})
}

func waitGone(ctx context.Context, rec record, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for alive(rec) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}
