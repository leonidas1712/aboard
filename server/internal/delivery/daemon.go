package delivery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Config is everything the daemon needs from outside.
type Config struct {
	Journal  Journal
	Adapters []Adapter
	// Connect returns the connection to one server. It is called once per server URL,
	// when the first agent on that server is bound.
	Connect func(serverURL string) Server
	Control Control
	// Processes checks that each open session's harness still runs. Nil means sessions
	// close only when their end hook says so.
	Processes Processes
	Clock     clock.Clock
	Log       *slog.Logger
	// PID is reported by the status operation.
	PID int
	// IdleExit overrides how long the daemon runs with no open session; zero means
	// the default.
	IdleExit time.Duration
}

// harnessCallTimeout bounds one call into a harness, such as one codex queue run.
const harnessCallTimeout = 30 * time.Second

// errIdle stops the daemon after it had no open session for IdleExit.
var errIdle = errors.New("no open session")

// Daemon delivers messages to the sessions open on this machine.
type Daemon struct {
	cfg      Config
	adapters map[string]Adapter
	log      *slog.Logger

	g   *errgroup.Group
	ctx context.Context

	// mu guards the routing tables below. A session's own state is never here: only
	// its goroutine touches it.
	mu       sync.Mutex
	sessions map[SessionKey]*session
	owners   map[AgentRef]*session
	servers  map[string]*serverConn
	open     map[SessionKey]bool
	problems map[AgentRef]string
	// modes holds each agent's delivery mode; an agent not in it is auto.
	modes map[AgentRef]Mode
	// openChanged fires when a session opens or closes.
	openChanged chan struct{}
}

// Run runs the daemon until ctx ends or it has had no open session for IdleExit.
func Run(ctx context.Context, cfg Config) error {
	if cfg.IdleExit == 0 {
		cfg.IdleExit = IdleExit
	}
	d := &Daemon{
		cfg: cfg, adapters: map[string]Adapter{}, log: cfg.Log,
		sessions: map[SessionKey]*session{}, owners: map[AgentRef]*session{},
		servers: map[string]*serverConn{}, open: map[SessionKey]bool{}, problems: map[AgentRef]string{},
		modes: map[AgentRef]Mode{}, openChanged: make(chan struct{}, 1),
	}
	for _, a := range cfg.Adapters {
		d.adapters[a.Harness()] = a
	}
	if n, err := cfg.Journal.RecoverHanded(ctx, cfg.Clock.Now()); err != nil {
		return fmt.Errorf("recover deliveries: %w", err)
	} else if n > 0 {
		d.log.Info("deliveries handed before a restart will be handed again", "count", n)
	}

	g, gctx := errgroup.WithContext(ctx)
	d.g, d.ctx = g, gctx
	if err := d.restore(gctx); err != nil {
		return err
	}
	g.Go(func() error { return d.acceptLoop(gctx) })
	g.Go(func() error { return d.idleLoop(gctx) })
	if cfg.Processes != nil {
		g.Go(func() error { return d.livenessLoop(gctx) })
	}
	g.Go(func() error {
		<-gctx.Done()
		return cfg.Control.Close()
	})
	err := g.Wait()
	if errors.Is(err, errIdle) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// restore loads the sessions, bindings and deliveries the journal holds.
func (d *Daemon) restore(ctx context.Context) error {
	records, err := d.cfg.Journal.Sessions(ctx)
	if err != nil {
		return fmt.Errorf("load sessions: %w", err)
	}
	bindings, err := d.cfg.Journal.Bindings(ctx)
	if err != nil {
		return fmt.Errorf("load bindings: %w", err)
	}
	deliveries, err := d.cfg.Journal.Deliveries(ctx, openStates...)
	if err != nil {
		return fmt.Errorf("load deliveries: %w", err)
	}
	modes, err := d.cfg.Journal.Modes(ctx)
	if err != nil {
		return fmt.Errorf("load delivery modes: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	maps.Copy(d.modes, modes)
	for _, r := range records {
		s := d.newSessionLocked(r.Key)
		if s == nil {
			continue
		}
		s.boot, s.open, s.proc = r.Boot, r.Open, r.Process
		d.open[r.Key] = r.Open
	}
	for _, b := range bindings {
		s := d.sessions[b.Session]
		if s == nil {
			continue
		}
		a := &agentState{ref: b.Agent, deliveries: map[int64]*Delivery{}}
		for i := range deliveries {
			if deliveries[i].Agent == b.Agent {
				dl := deliveries[i]
				a.deliveries[dl.ID] = &dl
			}
		}
		s.agents[b.Agent] = a
		d.owners[b.Agent] = s
		d.watchLocked(b.Agent)
	}
	for _, s := range d.sessions {
		s.restored = true
		d.startSession(s)
	}
	return nil
}

// openStates are every delivery state except done.
var openStates = []State{StatePending, StateHanded, StateConfirmed, StateRetry, StateHeld, StateAttention, StateSkipped}

func (d *Daemon) newSessionLocked(key SessionKey) *session {
	ad, ok := d.adapters[key.Harness]
	if !ok {
		return nil
	}
	s := &session{
		d: d, key: key, adapter: ad, mail: newMailbox[sessionMsg](),
		agents: map[AgentRef]*agentState{}, refreshing: map[int64]bool{}, forward: map[AgentRef]*session{},
	}
	d.sessions[key] = s
	return s
}

func (d *Daemon) startSession(s *session) {
	d.g.Go(func() error { return s.run(d.ctx) })
}

// session returns the session for key, creating it when create is true.
func (d *Daemon) session(key SessionKey, create bool) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.sessions[key]; ok {
		return s
	}
	if !create {
		return nil
	}
	s := d.newSessionLocked(key)
	if s != nil {
		d.startSession(s)
	}
	return s
}

// setOwner routes an agent's messages to s and returns the session that had it.
func (d *Daemon) setOwner(agent AgentRef, s *session) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	prev := d.owners[agent]
	d.owners[agent] = s
	d.watchLocked(agent)
	if prev == s {
		return nil
	}
	return prev
}

func (d *Daemon) owner(agent AgentRef) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.owners[agent]
}

// server returns the connection to a server, starting it on first use.
func (d *Daemon) server(url string) *serverConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.serverLocked(url)
}

func (d *Daemon) serverLocked(url string) *serverConn {
	if c, ok := d.servers[url]; ok {
		return c
	}
	c := &serverConn{d: d, url: url, srv: d.cfg.Connect(url), mail: newMailbox[srvMsg](), watched: map[AgentRef]bool{}}
	d.servers[url] = c
	d.g.Go(func() error { return c.run(d.ctx) })
	return c
}

func (d *Daemon) watchLocked(agent AgentRef) {
	d.serverLocked(agent.Server).mail.put(srvMsg{watch: &agent})
}

// setOpen records whether a session is open, for the idle timer and status.
func (d *Daemon) setOpen(key SessionKey, open bool) {
	d.mu.Lock()
	changed := d.open[key] != open
	d.open[key] = open
	d.mu.Unlock()
	if changed {
		select {
		case d.openChanged <- struct{}{}:
		default:
		}
	}
}

func (d *Daemon) openCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, o := range d.open {
		if o {
			n++
		}
	}
	return n
}

func (d *Daemon) setProblem(agent AgentRef, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if reason == "" {
		delete(d.problems, agent)
		return
	}
	d.problems[agent] = reason
}

// mode returns the agent's delivery mode.
func (d *Daemon) mode(agent AgentRef) Mode {
	d.mu.Lock()
	defer d.mu.Unlock()
	if m, ok := d.modes[agent]; ok {
		return m
	}
	return ModeAuto
}

// setMode answers OpMode: it shows the agent's delivery mode, or saves a new one and has
// the agent's session apply it at once.
func (d *Daemon) setMode(ctx context.Context, req Request) Response {
	if req.Agent == nil {
		return errorResponse("invalid_request", "A mode request needs an agent.", "Send the agent's server, board and name.")
	}
	agent := *req.Agent
	prev := d.mode(agent)
	if req.Mode == "" || req.Mode == prev {
		return Response{V: ProtocolVersion, Mode: prev}
	}
	if _, ok := ParseMode(string(req.Mode)); !ok {
		return errorResponse("invalid_request", fmt.Sprintf("%q is not a delivery mode.", req.Mode), "Use auto, humans or off.")
	}
	if err := d.cfg.Journal.SetMode(ctx, agent, req.Mode); err != nil {
		return errorResponse("internal", "Couldn't save the delivery mode: "+err.Error(), "Look at the daemon log.")
	}
	d.mu.Lock()
	d.modes[agent] = req.Mode
	owner := d.owners[agent]
	d.mu.Unlock()
	if owner != nil {
		owner.mail.put(sessionMsg{modeChanged: true})
	}
	d.log.Info("delivery mode changed", "agent", agent.Name, "board", agent.Board, "mode", req.Mode)
	return Response{V: ProtocolVersion, Mode: req.Mode, Changed: true}
}

// idleLoop stops the daemon once no session has been open for IdleExit.
func (d *Daemon) idleLoop(ctx context.Context) error {
	for {
		if d.openCount() > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-d.openChanged:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.openChanged:
		case <-d.cfg.Clock.After(d.cfg.IdleExit):
			if d.openCount() == 0 {
				d.log.Info("stopping: no open session", "for", d.cfg.IdleExit)
				return errIdle
			}
		}
	}
}

// livenessLoop asks every session to check its harness process every LivenessCheck.
func (d *Daemon) livenessLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.cfg.Clock.After(LivenessCheck):
		}
		d.mu.Lock()
		for _, s := range d.sessions {
			s.mail.put(sessionMsg{checkAlive: true})
		}
		d.mu.Unlock()
	}
}

func (d *Daemon) acceptLoop(ctx context.Context) error {
	for ctx.Err() == nil {
		conn, err := d.cfg.Control.Accept()
		if err == nil {
			d.g.Go(func() error {
				d.serve(ctx, conn)
				return nil
			})
			continue
		}
		if errors.Is(err, net.ErrClosed) {
			break
		}
		if ctx.Err() == nil {
			d.log.Warn("control socket: connection refused", "error", err)
		}
	}
	return nil
}

// serve answers one control connection.
func (d *Daemon) serve(ctx context.Context, conn net.Conn) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	var req Request
	if err := ReadFrame(r, &req); err != nil {
		if errors.Is(err, ErrFrameTooLarge) {
			_ = WriteFrame(conn, errorResponse("invalid_request", "The message to the delivery daemon is too large.", "Send messages of at most 128 KiB."))
		}
		return
	}
	if req.V != ProtocolVersion {
		_ = WriteFrame(conn, errorResponse("invalid_request",
			fmt.Sprintf("The delivery daemon speaks protocol version %d, not %d.", ProtocolVersion, req.V),
			"Use the same aboard binary for hooks and the daemon, or stop the running daemon so the current one starts."))
		return
	}
	switch req.Op {
	case OpStatus:
		_ = WriteFrame(conn, d.status(ctx))
	case OpMode:
		_ = WriteFrame(conn, d.setMode(ctx, req))
	case OpWait:
		d.serveWait(ctx, conn, r, req)
	case OpRegister, OpPrompt, OpTurnEnd, OpUrgent, OpEnd, OpBind, OpAgents:
		_ = WriteFrame(conn, d.call(ctx, req))
	default:
		_ = WriteFrame(conn, errorResponse("invalid_request", fmt.Sprintf("The delivery daemon has no operation %q.", req.Op),
			"Use the same aboard binary for hooks and the daemon."))
	}
}

func errorResponse(code, message, hint string) Response {
	return Response{V: ProtocolVersion, Error: &WireError{Code: code, Message: message, Hint: hint}}
}

func sessionUnknown(key SessionKey) Response {
	return errorResponse("session_unknown",
		"The delivery daemon doesn't know the session "+key.String()+".",
		"Install the delivery hooks with aboard init, then start a new session; or pass --as to pick an agent.")
}

// call routes one request to its session and returns the session's answer.
func (d *Daemon) call(ctx context.Context, req Request) Response {
	ad, ok := d.adapters[req.Harness]
	if !ok || req.Session == "" {
		return errorResponse("invalid_request", fmt.Sprintf("%q is not a harness the delivery daemon knows.", req.Harness),
			"Use claude-code or codex.")
	}
	key := req.Key()
	create := false
	switch req.Op {
	case OpRegister, OpBind:
		if req.Op == OpBind && req.Agent == nil {
			return errorResponse("invalid_request", "A bind request needs an agent.", "Send the agent's server, board and name.")
		}
		// A harness that confirms on handing (Codex) can be bound from any command run in
		// the session; one that needs hooks must have registered first.
		create = req.Op == OpRegister || !ad.WaitsForIdle()
		vctx, cancel := context.WithTimeout(ctx, harnessCallTimeout)
		err := ad.Validate(vctx, req.Session)
		cancel()
		if r, failed := validationResponse(key, err); failed {
			return r
		}
	}
	s := d.session(key, create)
	if s == nil {
		switch req.Op {
		case OpAgents, OpBind:
			if !ad.WaitsForIdle() && req.Op == OpAgents {
				return Response{V: ProtocolVersion}
			}
			return sessionUnknown(key)
		default:
			return Response{V: ProtocolVersion}
		}
	}
	reply := make(chan Response, 1)
	s.mail.put(sessionMsg{req: req, reply: reply})
	select {
	case r := <-reply:
		return r
	case <-ctx.Done():
		return errorResponse("daemon_not_running", "The delivery daemon is stopping.", "Run the command again; it starts the daemon.")
	}
}

// validationResponse turns an adapter's verdict on a session into an error response.
func validationResponse(key SessionKey, err error) (Response, bool) {
	switch {
	case err == nil:
		return Response{}, false
	case errors.Is(err, ErrSubAgent):
		return errorResponse(ReasonSubAgent,
			"This Codex thread ("+key.ID+") is a sub-agent, and messages can only go to the root conversation.",
			"Run aboard join or aboard resume in the root Codex conversation instead."), true
	case errors.Is(err, ErrTargetAbsent):
		return errorResponse(ReasonTargetAbsent,
			"Codex has no thread "+key.ID+".",
			"Run the command inside a Codex session, or open that thread again."), true
	default:
		return errorResponse("harness_unavailable",
			"Couldn't check the session "+key.String()+": "+err.Error(),
			"Check that the harness is installed and works, then run the command again."), true
	}
}

// serveWait keeps a stop hook's connection while its session is idle.
func (d *Daemon) serveWait(ctx context.Context, conn net.Conn, r *bufio.Reader, req Request) {
	s := d.session(req.Key(), false)
	if s == nil {
		_ = WriteFrame(conn, sessionUnknown(req.Key()))
		return
	}
	w := &waiter{conn: conn, received: make(chan struct{}, 1), gone: make(chan struct{})}
	s.mail.put(sessionMsg{req: req, waiter: w})
	for {
		var m Request
		if err := ReadFrame(r, &m); err != nil {
			break
		}
		if m.Op == OpReceived {
			select {
			case w.received <- struct{}{}:
			default:
			}
		}
	}
	close(w.gone)
	if ctx.Err() == nil {
		s.mail.put(sessionMsg{gone: w})
	}
}

// waiter is a stop hook waiting on its connection.
type waiter struct {
	mu       sync.Mutex
	conn     net.Conn
	received chan struct{}
	gone     chan struct{}
}

// Deliver sends the bundle and waits for the hook to say it has it.
func (w *waiter) Deliver(ctx context.Context, bundle string) error {
	err := w.write(Response{V: ProtocolVersion, Event: EventDeliver, Bundle: bundle})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	select {
	case <-w.received:
		return nil
	case <-w.gone:
		return ErrBusy
	case <-ctx.Done():
		return fmt.Errorf("wait for the hook to take the bundle: %w", ctx.Err())
	}
}

// waiterWriteTimeout bounds one write to a waiting hook, so a hook that stopped reading
// can't hold up its session.
const waiterWriteTimeout = 5 * time.Second

// write sends one frame to the hook.
func (w *waiter) write(r Response) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(waiterWriteTimeout))
	return WriteFrame(w.conn, r)
}

// accepted tells the hook its wait is registered.
func (w *waiter) accepted() { _ = w.write(Response{V: ProtocolVersion, Event: EventWaiting}) }

// Release tells the hook to exit without a bundle.
func (w *waiter) Release() { _ = w.write(Response{V: ProtocolVersion, Event: EventRelease}) }

// status reports the daemon's state for aboard doctor.
func (d *Daemon) status(ctx context.Context) Response {
	st := &Status{
		PID: d.cfg.PID, OpenSessions: d.openCount(), Servers: []ServerStatus{},
		Attention: []StatusItem{}, Skipped: []StatusItem{}, Agents: []AgentProblem{}, Bindings: []BindingStatus{},
	}
	d.mu.Lock()
	for url, c := range d.servers {
		connected, problem := c.snapshot()
		st.Servers = append(st.Servers, ServerStatus{URL: url, Connected: connected, Problem: problem})
	}
	for a, reason := range d.problems {
		st.Agents = append(st.Agents, AgentProblem{Agent: a, Reason: reason})
	}
	for a, s := range d.owners {
		st.Bindings = append(st.Bindings, BindingStatus{Agent: a, Session: s.key.String()})
	}
	d.mu.Unlock()
	slices.SortFunc(st.Servers, func(a, b ServerStatus) int { return strings.Compare(a.URL, b.URL) })
	slices.SortFunc(st.Agents, func(a, b AgentProblem) int { return compareAgents(a.Agent, b.Agent) })
	slices.SortFunc(st.Bindings, func(a, b BindingStatus) int { return compareAgents(a.Agent, b.Agent) })
	ds, err := d.cfg.Journal.Deliveries(ctx, StateAttention, StateSkipped)
	if err != nil {
		return errorResponse("internal", "Couldn't read the delivery journal: "+err.Error(), "Look at the daemon log.")
	}
	for _, dl := range ds {
		item := StatusItem{ID: dl.ID, Agent: dl.Agent, Seqs: dl.Seqs, Reason: dl.Reason}
		if dl.State == StateAttention {
			st.Attention = append(st.Attention, item)
		} else {
			st.Skipped = append(st.Skipped, item)
		}
	}
	return Response{V: ProtocolVersion, Status: st}
}

func compareAgents(a, b AgentRef) int {
	if c := strings.Compare(a.Server, b.Server); c != 0 {
		return c
	}
	if c := strings.Compare(a.Board, b.Board); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}
