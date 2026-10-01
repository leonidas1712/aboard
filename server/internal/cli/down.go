package cli

import (
	"bufio"
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/delivery/proctable"
)

// stopTimeout is how long aboard down waits for the server or the daemon to stop.
const stopTimeout = 10 * time.Second

// runDown stops the local server and the delivery daemon, and waits until both have
// stopped. Open sessions start the daemon again on their next hook.
func runDown(ctx context.Context, a *app, args []string) error {
	const use = "aboard down [--json]"
	fs := a.flags("down")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	srv := a.localServer()
	serverStopped := false
	if a.localRunning(ctx) {
		if err := stopAboard(ctx, localPID(p), func() bool { return !a.localRunning(ctx) }); err != nil {
			return &Error{
				Code:    "server_not_running",
				Message: "Something answers at " + srv.URL + ", but it isn't a local Aboard server this machine started: " + err.Error(),
				Hint:    "Stop that program yourself, or set ABOARD_LOCAL_ADDR to another address.",
			}
		}
		serverStopped = true
	}
	daemonStopped := false
	if st, _ := a.daemonStatus(ctx); st != nil {
		if err := stopAboard(ctx, st.PID, func() bool { st, _ := a.daemonStatus(ctx); return st == nil }); err != nil {
			return &Error{
				Code: "daemon_not_running", Message: "Couldn't stop the delivery daemon: " + err.Error(),
				Hint: "Look at the daemon log at " + p.daemonLog() + ".",
			}
		}
		daemonStopped = true
	}

	text := "Local Aboard and the delivery daemon weren't running.\n"
	switch {
	case serverStopped && daemonStopped:
		text = "Stopped local Aboard at " + srv.URL + " and the delivery daemon.\n"
	case serverStopped:
		text = "Stopped local Aboard at " + srv.URL + ". The delivery daemon wasn't running.\n"
	case daemonStopped:
		text = "Stopped the delivery daemon. Local Aboard wasn't running.\n"
	}
	a.emit(struct {
		Server        serverRef `json:"server"`
		ServerStopped bool      `json:"server_stopped"`
		DaemonStopped bool      `json:"daemon_stopped"`
	}{srv, serverStopped, daemonStopped}, text)
	return nil
}

// stopAboard sends SIGTERM to pid, but only if it is an aboard process, then waits
// until stopped reports true.
func stopAboard(ctx context.Context, pid int, stopped func() bool) error {
	if pid <= 0 {
		return fmt.Errorf("no process id recorded")
	}
	if name, ok := proctable.Name(pid); !ok || name != "aboard" {
		return fmt.Errorf("process %d is not aboard", pid)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal process %d: %w", pid, err)
	}
	deadline := time.Now().Add(stopTimeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !stopped() {
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d didn't stop within %s", pid, stopTimeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for process %d to stop: %w", pid, ctx.Err())
		case <-tick.C:
		}
	}
	return nil
}

// daemonStatus asks a running delivery daemon for its status, without starting one.
// It returns nil when no daemon answers.
func (a *app) daemonStatus(ctx context.Context) (*delivery.Status, error) {
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := control.Dial(ctx, p.socket())
	if err != nil {
		return nil, nil //nolint:nilerr // no daemon answering is an answer, not an error
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	if err := delivery.WriteFrame(c, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpStatus}); err != nil {
		return nil, nil //nolint:nilerr // a daemon that hung up isn't running
	}
	var resp delivery.Response
	if err := delivery.ReadFrame(bufio.NewReader(c), &resp); err != nil || resp.Status == nil {
		return nil, nil //nolint:nilerr // a daemon that doesn't answer isn't running
	}
	return resp.Status, nil
}
