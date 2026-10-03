package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/server"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// startTimeout is how long a background local server may take to answer.
const startTimeout = 10 * time.Second

// localRunning reports whether a server answers at the local address.
func (a *app) localRunning(ctx context.Context) bool {
	return a.serverAnswers(ctx, a.localServer())
}

// serverAnswers reports whether a server answers at srv.
func (a *app) serverAnswers(ctx context.Context, srv serverRef) bool {
	c, err := a.newClient(srv, "", time.Second)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = c.info(ctx)
	return err == nil
}

// ensureLocal starts the local server in the background unless it is already running.
// It reports whether it started it.
func (a *app) ensureLocal(ctx context.Context) (bool, error) {
	if err := a.replaceOutdatedLocal(ctx); err != nil {
		return false, err
	}
	if a.localRunning(ctx) {
		return false, nil
	}
	return true, a.startLocalAndWait(ctx)
}

// startLocalAndWait starts the local server in the background and waits until it
// answers.
func (a *app) startLocalAndWait(ctx context.Context) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	if err := a.startLocal(p); err != nil {
		return err
	}
	deadline := time.Now().Add(startTimeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !a.localRunning(ctx) {
		if time.Now().After(deadline) {
			return newError("server_not_running",
				fmt.Sprintf("The local server didn't start within %s.", startTimeout),
				"Look at the server log at "+p.serverLog()+" for the reason.")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the local server: %w", ctx.Err())
		case <-tick.C:
		}
	}
	return nil
}

// startLocal runs "aboard serve" as a detached process that outlives this command,
// with its output appended to the server log.
func (a *app) startLocal(p paths) error {
	exe, err := a.env.Executable()
	if err != nil {
		return fmt.Errorf("find the aboard binary to start the local server: %w", err)
	}
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return fmt.Errorf("create data directory %s: %w", p.data, err)
	}
	logFile, err := os.OpenFile(p.serverLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open server log %s: %w", p.serverLog(), err)
	}
	defer func() { _ = logFile.Close() }()
	// The server must keep running after this command exits, so it is not tied to a
	// context and gets a session of its own.
	cmd := &exec.Cmd{
		Path:        exe,
		Args:        []string{exe, "serve"},
		Stdout:      logFile,
		Stderr:      logFile,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return &Error{
			Code: "server_not_running", Message: "Couldn't start the local server.",
			Hint: "Run aboard serve in a terminal to see why.", Err: err,
		}
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach the local server process: %w", err)
	}
	return nil
}

// localPID reads the running local server's process id.
func localPID(p paths) int {
	data, err := os.ReadFile(filepath.Clean(p.pidFile()))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// runServe runs the local server in the foreground until interrupted.
func runServe(ctx context.Context, a *app, args []string) error {
	fs := a.flags("serve")
	if _, err := a.parse(fs, args, "aboard serve", 0, 0); err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	owner := a.env.Getenv("USER")
	if owner == "" {
		owner = "human"
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	b := currentBuild()
	err = server.Run(ctx, server.Options{
		Addr:           a.localAddr(),
		DataDir:        p.data,
		OwnerName:      owner,
		OwnerTokenPath: p.ownerToken(),
		Version:        b.Version,
		Commit:         b.Commit,
		CommitTime:     b.CommitTime,
		Log:            slog.New(slog.NewJSONHandler(a.env.Stderr, nil)),
	})
	if errors.Is(err, sqlite.ErrNewerSchema) {
		return dataNewer(p.data, err)
	}
	if err != nil {
		hint := "If another program uses the address, set ABOARD_LOCAL_ADDR to a free one."
		if p.home != "" && a.env.Getenv("ABOARD_LOCAL_ADDR") == "" {
			hint = "If another program uses the address, delete " + p.addrFile() +
				" so the next start picks a free port, or set ABOARD_LOCAL_ADDR to a free one."
		}
		return &Error{Code: "server_not_running", Message: "The local server stopped: " + err.Error(), Hint: hint, Err: err}
	}
	return nil
}

// runUp starts the local server in the background.
func runUp(ctx context.Context, a *app, args []string) error {
	const use = "aboard up [--json]"
	fs := a.flags("up")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	started, err := a.ensureLocal(ctx)
	if err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	srv := a.localServer()
	text := "Local Aboard is already running at " + srv.URL + "\n"
	switch r := a.localReplaced; {
	case started:
		text = "Started local Aboard at " + srv.URL + "\n"
	case r != nil:
		text = "Replaced local Aboard at " + srv.URL + ": it ran aboard " + buildLabel(r.From) + ", an older build\n"
	}
	a.emit(map[string]any{
		"server": srv, "started": started, "replaced": a.localReplaced, "pid": localPID(p),
		"ui_url": srv.URL + "/", "data_dir": p.data, "version": version,
	}, text)
	return nil
}
