package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
	"github.com/leonidas1712/aboard/server/internal/delivery/proctable"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

// daemonStartTimeout is how long a background delivery daemon may take to answer.
const daemonStartTimeout = 5 * time.Second

// daemonCallTimeout bounds one request to the daemon. Binding a session can run the
// harness to check it (Codex's app server), which can take a few seconds.
const daemonCallTimeout = 60 * time.Second

// runDaemon runs the delivery daemon in the foreground until interrupted, or until no
// session has been open for ten minutes. A second daemon exits at once. "aboard daemon
// start" starts it in the background instead.
func runDaemon(ctx context.Context, a *app, args []string) error {
	use := usageOf("daemon")
	fs := a.flags("daemon")
	pos, err := a.parse(fs, args, use, 0, 1)
	if err != nil {
		return err
	}
	if len(pos) == 1 {
		if pos[0] != "start" {
			return usageError(fmt.Sprintf("%q is not a daemon command.", pos[0]), use)
		}
		return a.daemonStart(ctx)
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	if err := control.PrepareDir(p.state); err != nil {
		return socketUnsafe(p, err)
	}
	if err := control.PrepareDir(filepath.Dir(p.socket())); err != nil {
		return socketUnsafe(p, err)
	}
	lock, err := os.OpenFile(filepath.Clean(p.daemonLock()), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon lock %s: %w", p.daemonLock(), err)
	}
	defer func() { _ = lock.Close() }() // closing the file releases the lock
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil // another daemon runs; it does the work
		}
		return fmt.Errorf("lock %s: %w", p.daemonLock(), err)
	}
	pid := os.Getpid()
	if err := writeFileAtomic(p.daemonPID(), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return err
	}
	defer func() {
		if readPID(p.daemonPID()) == pid {
			_ = os.Remove(p.daemonPID())
		}
	}()
	log := slog.New(slog.NewJSONHandler(a.env.Stderr, nil))
	journal, err := sqlitejournal.Open(ctx, p.deliveryDB())
	if errors.Is(err, sqlitejournal.ErrNewerSchema) {
		return dataNewer(p.deliveryDB(), err)
	}
	if err != nil {
		return fmt.Errorf("open the delivery journal: %w", err)
	}
	defer func() { _ = journal.Close() }()
	listener, err := control.Listen(p.socket())
	if err != nil {
		if errors.Is(err, control.ErrUnsafe) {
			return socketUnsafe(p, err)
		}
		return &Error{
			Code: "daemon_not_running", Message: "The delivery daemon couldn't open its socket: " + err.Error(),
			Hint: "Look at the daemon log at " + p.daemonLog() + ".", Err: err,
		}
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("delivery daemon started", "pid", pid, "socket", p.socket())
	var adapters []delivery.Adapter
	for _, h := range a.registry() {
		if ad := h.Adapter(version); ad != nil {
			adapters = append(adapters, ad)
		}
	}
	var pairingMu sync.Mutex
	pairings := map[string]delivery.PairingRuntime{}
	err = delivery.Run(ctx, delivery.Config{
		PairingFor: func(server string) delivery.PairingRuntime {
			pairingMu.Lock()
			defer pairingMu.Unlock()
			if pairings[server] == nil {
				pairings[server] = apiserver.NewPairing(server, daemonTokens{a: a}, filepath.Join(p.state, "pairing-endpoints"))
			}
			return pairings[server]
		},
		AllowBriefNudge: a.allowBriefNudge,
		Journal:         journal,
		ResolveAgent:    (daemonTokens{a: a}).ResolveAgent,
		Adapters:        adapters,
		Connect: func(url string) delivery.Server {
			return apiserver.New(url, daemonTokens{a: a}, a.env.Rand)
		},
		Control:   listener,
		Processes: proctable.Table{},
		Clock:     clock.Real{},
		Log:       log,
		PID:       pid,
		Build:     currentBuild(),
		Tickets:   launchtickets.Dir(p.launches()),
		Seats:     newDaemonSeats(a),
	})
	log.Info("delivery daemon stopped", "error", errText(err))
	if err != nil {
		return fmt.Errorf("delivery daemon: %w", err)
	}
	return nil
}

// daemonStart starts the delivery daemon in the background, unless it runs already.
func (a *app) daemonStart(ctx context.Context) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	already := false
	if c, err := control.Dial(ctx, p.socket()); err == nil {
		_ = c.Close()
		already = true
	}
	if !already {
		if e := a.sandboxDaemonError(); e != nil {
			return e
		}
	}
	c, err := a.dialDaemon(ctx)
	if err != nil {
		return err
	}
	_ = c.Close()
	pid := readPID(p.daemonPID())
	text := fmt.Sprintf("Started the delivery daemon (pid %d).\n", pid)
	if already {
		text = fmt.Sprintf("The delivery daemon is already running (pid %d).\n", pid)
	}
	a.emit(struct {
		PID     int  `json:"pid"`
		Started bool `json:"started"`
	}{pid, !already}, text)
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func socketUnsafe(p paths, err error) *Error {
	return &Error{
		Code:    "socket_unsafe",
		Message: "The delivery daemon's socket directory can be opened by other users: " + err.Error(),
		Hint:    "Remove " + filepath.Dir(p.socket()) + "; it is created again with the right permissions.",
		Err:     err,
	}
}

// daemonTokens reads the logins the daemon sends to servers from this machine's files
// each time a request needs one.
type daemonTokens struct{ a *app }

// AgentToken returns an agent's token from credentials.json.
func (t daemonTokens) AgentToken(agent delivery.AgentRef) (string, error) {
	creds, err := t.a.readCredentials()
	if err != nil {
		return "", err
	}
	cred, ok := creds.forSeat(agent)
	if !ok || cred.Token == "" {
		return "", fmt.Errorf("%w: no token for %s on %s", delivery.ErrUnauthorized, agent.Name, agent.Board)
	}
	return cred.Token, nil
}

// ResolveAgent proves a saved credential's seat before any delivery state uses it.
func (t daemonTokens) ResolveAgent(ctx context.Context, agent delivery.AgentRef) (delivery.AgentRef, error) {
	denied := func() (delivery.AgentRef, error) {
		return delivery.AgentRef{}, fmt.Errorf("%w: the saved token does not identify this seat", delivery.ErrUnauthorized)
	}
	srv, err := parseServerURL(agent.Server)
	if err != nil || srv.URL != agent.Server {
		return denied()
	}
	creds, err := t.a.readCredentials()
	if err != nil {
		return delivery.AgentRef{}, err
	}
	cred, ok := creds.forSeat(agent)
	if !ok || cred.Token == "" || (agent.Board != "" && cred.Board != agent.Board) {
		return denied()
	}
	c, err := t.a.newClient(srv, cred.Token, requestTimeout)
	if err != nil {
		return delivery.AgentRef{}, err
	}
	r, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return delivery.AgentRef{}, c.unreachable(err)
	}
	if r.JSON200 == nil {
		if e := apiError(r.StatusCode(), r.Body); e.Code == "agent_removed" {
			// The seat was removed: final, like a board that is gone.
			return delivery.AgentRef{}, fmt.Errorf("%w: %s", delivery.ErrBoardGone, e.Message)
		}
		if r.StatusCode() == http.StatusUnauthorized || r.StatusCode() == http.StatusForbidden {
			return denied()
		}
		return delivery.AgentRef{}, apiError(r.StatusCode(), r.Body)
	}
	me := r.JSON200
	if me.Kind != api.MeKindAgent || !strings.HasPrefix(me.Id, "mem_") || me.Board == nil ||
		*me.Board != cred.Board || (cred.MemberID != "" && cred.MemberID != me.Id) ||
		(agent.MemberID != "" && agent.MemberID != me.Id) ||
		(cred.MemberID == "" && me.Name != cred.Name) {
		return delivery.AgentRef{}, delivery.ErrSeatMismatch
	}
	resolved := delivery.AgentRef{Server: cred.Server, Board: *me.Board, Name: me.Name, MemberID: me.Id}
	p, err := t.a.paths()
	if err != nil {
		return delivery.AgentRef{}, err
	}
	var current credentials
	err = updateJSONFile(p.credentials(), &current, func() error {
		now, exists := current.forSeat(agent)
		if !exists || now.Token != cred.Token {
			return delivery.ErrUnauthorized
		}
		if now.MemberID == "" {
			now.Legacy = &legacyCredentialIdentity{Board: now.Board, Name: now.Name}
		}
		now.MemberID, now.Board, now.Name = me.Id, *me.Board, me.Name
		current.put(now)
		return nil
	})
	if err != nil {
		return delivery.AgentRef{}, err
	}
	return resolved, nil
}

// HumanToken returns this machine's person's access key for the server at url: the
// local owner's for the local server, the one aboard connect saved for another. A key
// only ever goes to the server that issued it.
func (t daemonTokens) HumanToken(url string) (string, error) {
	token, err := t.a.readOwnerToken(t.a.serverRefFor(url))
	if err != nil {
		return "", fmt.Errorf("%w: %w", delivery.ErrLoginMissing, err)
	}
	return token, nil
}

func readPID(path string) int {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// dialDaemon connects to the delivery daemon, starting it in the background if it
// isn't running.
func (a *app) dialDaemon(ctx context.Context) (net.Conn, error) {
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	a.replaceOutdatedDaemon(ctx, p)
	if c, err := control.Dial(ctx, p.socket()); err == nil {
		return c, nil
	}
	if e := a.sandboxDaemonError(); e != nil {
		return nil, e
	}
	if err := a.startDaemon(p); err != nil {
		return nil, err
	}
	return a.waitDaemon(ctx, p)
}

// waitDaemon connects to a delivery daemon that is starting, waiting for it to answer.
func (a *app) waitDaemon(ctx context.Context, p paths) (net.Conn, error) {
	deadline := time.Now().Add(daemonStartTimeout)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		c, err := control.Dial(ctx, p.socket())
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, &Error{
				Code:    "daemon_not_running",
				Message: fmt.Sprintf("The delivery daemon didn't start within %s.", daemonStartTimeout),
				Hint:    "Look at the daemon log at " + p.daemonLog() + " for the reason, or run aboard doctor.",
				Err:     err,
			}
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for the delivery daemon: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// startDaemon runs "aboard daemon" as a detached process that outlives this command,
// with its output appended to the daemon log.
func (a *app) startDaemon(p paths) error {
	exe, err := a.env.Executable()
	if err != nil {
		return fmt.Errorf("find the aboard binary to start the delivery daemon: %w", err)
	}
	if err := os.MkdirAll(p.state, 0o700); err != nil {
		return fmt.Errorf("create state directory %s: %w", p.state, err)
	}
	logFile, err := os.OpenFile(p.daemonLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log %s: %w", p.daemonLog(), err)
	}
	defer func() { _ = logFile.Close() }()
	// The daemon must keep running after this command exits, so it is not tied to a
	// context and gets a session of its own.
	cmd := &exec.Cmd{
		Path:        exe,
		Args:        []string{exe, "daemon"},
		Dir:         p.state,
		Stdout:      logFile,
		Stderr:      logFile,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return &Error{
			Code: "daemon_not_running", Message: "Couldn't start the delivery daemon.",
			Hint: "Run aboard daemon in a terminal to see why.", Err: err,
		}
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach the delivery daemon process: %w", err)
	}
	return nil
}

// callDaemon sends one request to the daemon and returns its answer. An error the
// daemon reports comes back as an *Error with its code.
func (a *app) callDaemon(ctx context.Context, req delivery.Request) (delivery.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	c, err := a.dialDaemon(ctx)
	if err != nil {
		return delivery.Response{}, err
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	req.V = delivery.ProtocolVersion
	req = withHarnessProcess(req)
	if err := delivery.WriteFrame(c, req); err != nil {
		return delivery.Response{}, daemonGone(err)
	}
	var resp delivery.Response
	if err := delivery.ReadFrame(bufio.NewReader(c), &resp); err != nil {
		return delivery.Response{}, daemonGone(err)
	}
	if resp.Error != nil {
		hint := resp.Error.Hint
		if resp.Error.Code == "session_on_another_server" {
			hint = "This daemon supports only one issuer per session; upgrade Aboard and restart the daemon before retrying. Existing seats were kept."
		}
		return resp, &Error{Code: resp.Error.Code, Message: resp.Error.Message, Hint: hint, Details: resp.Error.Details}
	}
	return resp, nil
}

// withHarnessProcess adds the harness process this command runs under to a request
// about a session, so the daemon can close the session if that process dies without
// running its end hook.
func withHarnessProcess(req delivery.Request) delivery.Request {
	if req.Session == "" || req.Process != nil {
		return req
	}
	if p, ok := proctable.Harness(); ok {
		req.Process = &p
	}
	return req
}

func daemonGone(err error) *Error {
	return &Error{
		Code: "daemon_not_running", Message: "The delivery daemon closed the connection before answering.",
		Hint: "Run the command again; if it keeps failing, run aboard doctor.", Err: err,
	}
}
