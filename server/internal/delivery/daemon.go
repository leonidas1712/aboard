package delivery

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Journal Journal
	// ResolveAgent verifies a seat using its saved credential. Nil is used by
	// adapters whose agent references are already trusted, such as contract fixtures.
	ResolveAgent func(context.Context, AgentRef) (AgentRef, error)
	Adapters     []Adapter
	// Connect returns the connection to one server. It is called once per server URL,
	// when the first agent on that server is bound.
	Connect func(serverURL string) Server
	Control Control
	// Processes checks that each open session's harness still runs. Nil means sessions
	// close only when their end hook says so.
	Processes Processes
	Clock     clock.Clock
	Log       *slog.Logger
	// PID and Build are reported by the status operation.
	PID   int
	Build Build
	// IdleExit overrides how long the daemon runs with no open session; zero means
	// the default.
	IdleExit time.Duration
	// Tickets holds the launch tickets aboard swarm up writes. Nil means a launch
	// ticket binds nothing.
	Tickets Tickets
	// Seats lists and joins boards for sessions through the machine's delegation
	// (seats.go). Nil means the boards and join operations refuse.
	Seats Seats
	// joinHooks are set only by tests (export_test.go).
	joinHooks *joinHooks
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
	owners   map[AgentKey]*session
	refs     map[AgentKey]AgentRef
	servers  map[string]*serverConn
	open     map[SessionKey]bool
	// turned holds the sessions that have run a turn, for status.
	turned            map[SessionKey]bool
	problems          map[AgentKey]string
	extensionProblems map[SessionKey]bool
	generations       map[AgentKey]uint64
	// modes holds each agent's delivery mode as the journal keeps it: one set on this
	// machine, or the last one read from its server. An agent not in it has the default.
	modes map[AgentKey]Mode
	// held holds each agent's delivery mode as its server holds it, once read from the
	// server in this run. It wins over modes.
	held map[AgentKey]HeldMode
	// stalled are the deliveries handed to an idle session that started no turn, by id.
	stalled map[int64]StatusItem
	// openChanged fires when a session opens or closes.
	openChanged chan struct{}
	// joining holds one turn per session, so a session's joins, and so joins for one seat,
	// never race (seats.go).
	joining map[string]chan struct{}
}

// Run runs the daemon until ctx ends or it has had no open session for IdleExit.
func Run(ctx context.Context, cfg Config) error {
	if cfg.IdleExit == 0 {
		cfg.IdleExit = IdleExit
	}
	d := &Daemon{
		cfg: cfg, adapters: map[string]Adapter{}, log: cfg.Log,
		refs: map[AgentKey]AgentRef{}, sessions: map[SessionKey]*session{}, owners: map[AgentKey]*session{},
		servers: map[string]*serverConn{}, open: map[SessionKey]bool{}, turned: map[SessionKey]bool{}, problems: map[AgentKey]string{},
		generations: map[AgentKey]uint64{}, modes: map[AgentKey]Mode{}, held: map[AgentKey]HeldMode{}, stalled: map[int64]StatusItem{}, openChanged: make(chan struct{}, 1),
	}
	for _, a := range cfg.Adapters {
		d.adapters[a.Harness()] = a
	}
	g, gctx := errgroup.WithContext(ctx)
	d.g, d.ctx = g, gctx
	if err := d.restore(gctx); err != nil {
		return err
	}
	g.Go(func() error { return d.acceptLoop(gctx) })
	g.Go(func() error { return d.idleLoop(gctx) })
	g.Go(func() error { return d.presenceLoop(gctx) })
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
	blocked, retry, err := d.resolveJournal(ctx)
	if err != nil {
		return err
	}
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
	if err := d.recoverHanded(ctx, records, deliveries); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for a, m := range modes {
		// A mode an earlier build saved as auto is all.
		if parsed, ok := ParseMode(string(m)); ok {
			d.modes[a.Key()] = parsed
			d.rememberLocked(a)
		}
	}
	for _, r := range records {
		s := d.newSessionLocked(r.Key)
		if s == nil {
			continue
		}
		s.boot, s.open, s.proc, s.lost, s.started, s.turned = r.Boot, r.Open, r.Process, r.Lost, true, r.Turned
		d.open[r.Key] = r.Open
		d.turned[r.Key] = r.Turned
	}
	for _, b := range bindings {
		if reason, ended := blocked[b.Agent.Key()]; ended {
			d.rememberLocked(b.Agent)
			d.problems[b.Agent.Key()] = reason
			continue
		}
		s := d.sessions[b.Session]
		if s == nil {
			continue
		}
		a := newAgentState(b.Agent, false)
		a.generation = b.Generation
		d.generations[b.Agent.Key()] = b.Generation
		// What a session was told before the daemon stopped isn't kept: it is taken to
		// know the mode its agent has when its inbox is first read (onInbox).
		for i := range deliveries {
			if deliveries[i].Agent.Key() == b.Agent.Key() {
				dl := deliveries[i]
				a.deliveries[dl.ID] = &dl
			}
		}
		s.agents[b.Agent.Key()] = a
		d.rememberLocked(b.Agent)
		d.owners[b.Agent.Key()] = s
		d.watchLocked(b.Agent)
	}
	manifests, err := d.cfg.Journal.Handoffs(ctx)
	if err != nil {
		return fmt.Errorf("load handoffs: %w", err)
	}
	for _, s := range d.sessions {
		s.restoreHandoffs(manifests)
		s.restored = true
		d.startSession(s)
	}
	for _, b := range retry {
		d.g.Go(func() error {
			d.retryJournalSeat(d.ctx, b)
			return nil
		})
	}
	return nil
}

// retryJournalSeat keeps trying, with backoff, to prove a journal seat whose server
// couldn't be reached as the daemon started, and gives it back to its session once the
// server answers. A refusal (unauthorized, board gone) is final, as it is at start.
func (d *Daemon) retryJournalSeat(ctx context.Context, b Binding) {
	for failures := 1; ; failures++ {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(backoff(failures)):
		}
		resolved, err := d.resolveAgent(ctx, b.Agent)
		if err == nil {
			d.log.Info("journal seat verified after its server came back", "server", resolved.Server, "board", resolved.Board, "agent", resolved.Name)
			d.mu.Lock()
			s := d.sessions[b.Session]
			d.mu.Unlock()
			if s != nil {
				s.mail.put(sessionMsg{restoreSeat: &resolved})
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		if reason := problemOf(err); reason != "" {
			d.setProblem(b.Agent, reason)
			d.log.Warn("resolve journal seat", "server", b.Agent.Server, "board", b.Agent.Board, "agent", b.Agent.Name, "error", err)
			return
		}
		d.log.Info("journal seat's server still unreachable", "server", b.Agent.Server, "board", b.Agent.Board, "agent", b.Agent.Name,
			"retry_in", backoff(failures+1), "error", err)
	}
}

// resolveJournal proves each journal seat with its saved credential before the daemon
// restores it. It returns the seats that stay stopped, with why, and the unverified
// seats whose server couldn't be reached: those wait, marked server_unreachable, until
// retryJournalSeat proves them.
func (d *Daemon) resolveJournal(ctx context.Context) (map[AgentKey]string, []Binding, error) {
	blocked := map[AgentKey]string{}
	var retry []Binding
	if d.cfg.ResolveAgent == nil {
		return blocked, nil, nil
	}
	bindings, err := d.cfg.Journal.Bindings(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("read bindings for identity resolution: %w", err)
	}
	seen := map[AgentKey]bool{}
	for _, b := range bindings {
		if seen[b.Agent.Key()] {
			continue
		}
		seen[b.Agent.Key()] = true
		resolved, err := d.resolveAgent(ctx, b.Agent)
		if err != nil {
			if b.Agent.MemberID != "" && !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrBoardGone) {
				d.log.Warn("seat identity server unavailable; keeping verified journal identity", "server", b.Agent.Server, "member_id", b.Agent.MemberID)
				continue
			}
			reason := problemOf(err)
			if reason == "" {
				// The server couldn't be reached or failed. That passes, so the seat
				// waits for it rather than stopping for good.
				reason = ReasonServerUnreachable
				retry = append(retry, b)
			}
			blocked[b.Agent.Key()] = reason
			d.log.Warn("resolve journal seat", "server", b.Agent.Server, "board", b.Agent.Board, "agent", b.Agent.Name, "error", err)
		} else if b.Agent.MemberID != "" && resolved != b.Agent {
			b.Agent = resolved
			if err := d.cfg.Journal.Bind(ctx, b); err != nil {
				return nil, nil, fmt.Errorf("refresh restored seat metadata: %w", err)
			}
		}
	}
	return blocked, retry, nil
}

func (d *Daemon) resolveAgent(ctx context.Context, agent AgentRef) (AgentRef, error) {
	if d.cfg.ResolveAgent == nil || agent == (AgentRef{}) {
		return agent, nil
	}
	rctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
	defer cancel()
	resolved, err := d.cfg.ResolveAgent(rctx, agent)
	if err != nil {
		return AgentRef{}, err
	}
	if resolved.Server != agent.Server || resolved.Board != agent.Board || resolved.MemberID == "" ||
		(agent.MemberID != "" && resolved.MemberID != agent.MemberID) {
		return AgentRef{}, fmt.Errorf("%w: saved credential does not prove this seat", ErrSeatMismatch)
	}
	if agent.MemberID == "" {
		if err := d.cfg.Journal.ResolveIdentity(ctx, agent, resolved); err != nil {
			return AgentRef{}, fmt.Errorf("resolve journal identity: %w", err)
		}
		d.mu.Lock()
		if mode, ok := d.modes[agent.Key()]; ok {
			if _, known := d.modes[resolved.Key()]; !known {
				d.modes[resolved.Key()] = mode
			}
			delete(d.modes, agent.Key())
		}
		delete(d.problems, agent.Key())
		delete(d.refs, agent.Key())
		d.rememberLocked(resolved)
		d.mu.Unlock()
	}
	return resolved, nil
}

func (d *Daemon) resolveRequest(ctx context.Context, req Request) (Request, *Response) {
	if req.Agent == nil {
		return req, nil
	}
	resolved, err := d.resolveAgent(ctx, *req.Agent)
	if err != nil {
		r := errorResponse("server_unreachable", "The agent's seat could not be verified right now.", "Check the server connection and try again; the existing binding is unchanged.")
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrBoardGone) {
			r = errorResponse("unauthorized", "This machine cannot verify the agent's seat.",
				"Run aboard status; use the seat's current credentials or ask your person to add a new agent.")
		}
		if errors.Is(err, ErrSeatMismatch) {
			r = errorResponse("invalid_request", "The member_id does not match the seat's own credential.",
				"Use the seat identity returned by aboard status; the existing binding is unchanged.")
		}
		return req, &r
	}
	req.Agent = &resolved
	return req, nil
}

// recoverHanded decides, as the daemon starts, what happens to deliveries an earlier
// daemon handed over and never saw confirmed. A bundle a waiting hook took has reached
// the session, which may still be running the turn it woke: after an upgrade that
// turn's own hooks replace the old daemon. So it stays handed, and the session's next
// event from the same process confirms it, as it would have before the restart. A hook
// that was still waiting reconnects with a resumed wait, which hands it again (onWait).
// Everything else goes back to pending: a queueing harness's call may not have
// finished, and a closed session never confirms.
func (d *Daemon) recoverHanded(ctx context.Context, records []SessionRecord, deliveries []Delivery) error {
	open := map[SessionKey]bool{}
	for _, r := range records {
		open[r.Key] = r.Open
	}
	n := 0
	for i := range deliveries {
		dl := &deliveries[i]
		if dl.State != StateHanded {
			continue
		}
		if a, ok := d.adapters[dl.Session.Harness]; ok && a.WaitsForIdle() && open[dl.Session] {
			continue
		}
		dl.State, dl.UpdatedAt = StatePending, d.cfg.Clock.Now()
		if err := d.cfg.Journal.UpdateDelivery(ctx, *dl); err != nil {
			return fmt.Errorf("recover delivery %d: %w", dl.ID, err)
		}
		n++
	}
	if n > 0 {
		d.log.Info("deliveries handed before a restart will be handed again", "count", n)
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
		agents: map[AgentKey]*agentState{}, refreshing: map[int64]bool{}, forward: map[AgentKey]*session{},
		holds: map[*hold]bool{}, readers: map[*reading]bool{},
		reported: map[AgentKey]reportedPresence{},
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
	d.rememberLocked(agent)
	prev := d.owners[agent.Key()]
	d.owners[agent.Key()] = s
	d.watchLocked(agent)
	if prev == s {
		return nil
	}
	return prev
}

// dropOwner stops routing an agent's messages to s, unless another session has taken
// the agent since.
func (d *Daemon) dropOwner(agent AgentRef, s *session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.owners[agent.Key()] == s {
		delete(d.owners, agent.Key())
	}
}

func (d *Daemon) owner(agent AgentRef) *session {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.owners[agent.Key()]
}

func (d *Daemon) rememberLocked(agent AgentRef) {
	if d.refs == nil {
		d.refs = map[AgentKey]AgentRef{}
	}
	d.refs[agent.Key()] = agent
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
	c := &serverConn{d: d, url: url, srv: d.cfg.Connect(url), mail: newMailbox[srvMsg](), watched: map[AgentKey]AgentRef{}, gone: map[AgentKey]bool{}}
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

// setTurned records, for status, that a session has run a turn.
func (d *Daemon) setTurned(key SessionKey) {
	d.mu.Lock()
	d.turned[key] = true
	d.mu.Unlock()
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

// ReasonNoTurn is why a delivery stalled: its idle session started no turn.
const ReasonNoTurn = "no_turn_started"

// setStalled records that a delivery stalled, or no longer has, for status.
func (d *Daemon) setStalled(dl *Delivery, stalled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !stalled {
		delete(d.stalled, dl.ID)
		return
	}
	d.stalled[dl.ID] = StatusItem{ID: dl.ID, Agent: dl.Agent, Seqs: slices.Clone(dl.Seqs), Reason: ReasonNoTurn}
}

func (d *Daemon) setProblem(agent AgentRef, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if reason == "" {
		delete(d.problems, agent.Key())
		return
	}
	d.rememberLocked(agent)
	d.problems[agent.Key()] = reason
}

// mode returns the agent's delivery mode: the one its server holds, once read; else the
// one the journal keeps for it, else the machine's default (kept under the empty
// AgentRef), else focused. The journal's are what a server that doesn't hold modes
// goes by, and what the daemon goes by after a restart until it reads the server again.
func (d *Daemon) mode(agent AgentRef) Mode {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.modeLocked(agent)
}

// heldMode reports whether the daemon has read the agent's mode from its server.
func (d *Daemon) heldMode(agent AgentRef) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.held[agent.Key()]
	return ok
}

// modeLocked is mode, for a caller that holds d.mu.
func (d *Daemon) modeLocked(agent AgentRef) Mode {
	if h, ok := d.held[agent.Key()]; ok {
		return h.Mode
	}
	if m, ok := d.modes[agent.Key()]; ok {
		return m
	}
	if m, ok := d.modes[AgentKey{}]; ok {
		return m
	}
	return ModeFocused
}

// learnMode takes an agent's delivery mode as its server holds it, read with its inbox
// or passed on by the command that set it there. A read older than one already taken
// (a lower revision) changes nothing, since reads can arrive out of order. A mode its
// person set is kept in the journal too, for the daemon's next start; one never set
// (revision 0) leaves the journal as it is, so aboard doctor can still name a mode set
// on this machine that the server doesn't have. It reports whether the mode in force
// changed.
func (d *Daemon) learnMode(ctx context.Context, agent AgentRef, h HeldMode) bool {
	if parsed, ok := ParseMode(string(h.Mode)); ok {
		h.Mode = parsed
	} else {
		h.Mode = ModeFocused
	}
	d.mu.Lock()
	prev, known := d.held[agent.Key()]
	if known && h.Revision < prev.Revision {
		d.mu.Unlock()
		return false
	}
	before := d.modeLocked(agent)
	cached, inJournal := d.modes[agent.Key()]
	save := h.Revision > 0 && (!inJournal || cached != h.Mode)
	if save {
		// Keep the persisted cache unchanged on failure, so an identical read retries.
		if err := d.cfg.Journal.SetMode(ctx, agent, h.Mode); err != nil {
			d.log.Warn("save the delivery mode read from the server", "agent", agent.Name, "board", agent.Board, "error", err)
		} else {
			d.modes[agent.Key()] = h.Mode
		}
		d.rememberLocked(agent)
	}
	d.held[agent.Key()] = h
	d.mu.Unlock()
	if before == h.Mode {
		return false
	}
	d.log.Info("delivery mode changed on the server", "agent", agent.Name, "board", agent.Board,
		"from", before, "to", h.Mode, "revision", h.Revision)
	return true
}

// setMode answers OpMode: it shows the agent's delivery mode, or takes a new one and has
// the agent's session apply it at once. A mode with a revision is the one the agent's
// server now holds (the command that set it there passes it on); one without is kept on
// this machine only, and applies while the server holds none. The empty AgentRef names
// the default for agents without a mode of their own.
func (d *Daemon) setMode(ctx context.Context, req Request) Response {
	if req.Agent == nil {
		return errorResponse("invalid_request", "A mode request needs an agent.", "Send the agent's server, board and name.")
	}
	agent := *req.Agent
	prev := d.mode(agent)
	if req.Mode == "" {
		return Response{V: ProtocolVersion, Mode: prev}
	}
	mode, ok := ParseMode(string(req.Mode))
	if !ok {
		return errorResponse("invalid_request", fmt.Sprintf("%q is not a delivery mode.", req.Mode), "Use focused, all, humans or off.")
	}
	if req.Revision > 0 && agent != (AgentRef{}) {
		changed := d.learnMode(ctx, agent, HeldMode{Mode: mode, Revision: req.Revision})
		if s := d.owner(agent); s != nil && changed {
			s.mail.put(sessionMsg{modeChanged: true})
		}
		return Response{V: ProtocolVersion, Mode: d.mode(agent), Changed: changed}
	}
	if mode == prev {
		return Response{V: ProtocolVersion, Mode: prev}
	}
	req.Mode = mode
	if err := d.cfg.Journal.SetMode(ctx, agent, req.Mode); err != nil {
		return errorResponse("internal", "Couldn't save the delivery mode: "+err.Error(), "Look at the daemon log.")
	}
	d.mu.Lock()
	d.rememberLocked(agent)
	d.modes[agent.Key()] = req.Mode
	var owners []*session
	for a, s := range d.owners {
		if _, own := d.modes[a]; a == agent.Key() || (agent == AgentRef{} && !own) {
			owners = append(owners, s)
		}
	}
	now := d.modeLocked(agent)
	d.mu.Unlock()
	for _, s := range owners {
		s.mail.put(sessionMsg{modeChanged: true})
	}
	d.log.Info("delivery mode changed on this machine", "agent", agent.Name, "board", agent.Board, "mode", req.Mode)
	// A server that holds the agent's mode decides it, so the answer is the mode in force.
	return Response{V: ProtocolVersion, Mode: now, Changed: now == req.Mode}
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

// presenceLoop asks every session to report its agents' presence again every
// PresenceRenew, so the server doesn't let it run out while it holds.
func (d *Daemon) presenceLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-d.cfg.Clock.After(PresenceRenew):
		}
		d.mu.Lock()
		for _, s := range d.sessions {
			s.mail.put(sessionMsg{renewPresence: true})
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
		_ = WriteFrame(conn, errorResponse("daemon_protocol_mismatch",
			fmt.Sprintf("The running delivery daemon (aboard %s) speaks control protocol %d; this aboard speaks %d.", d.cfg.Build.Version, ProtocolVersion, req.V),
			"Install the same aboard as the running daemon, or run aboard down so this one starts its own."))
		return
	}
	if req.Op == OpMode || req.Op == OpHold || req.Op == OpInbox {
		var refusal *Response
		req, refusal = d.resolveRequest(ctx, req)
		if refusal != nil {
			_ = WriteFrame(conn, *refusal)
			return
		}
	}
	switch req.Op {
	case OpStatus:
		_ = WriteFrame(conn, d.status(ctx))
	case OpMode:
		_ = WriteFrame(conn, d.setMode(ctx, req))
	case OpWait:
		d.serveWait(ctx, conn, r, req)
	case OpHold:
		d.serveHold(ctx, conn, r, req)
	case OpInbox:
		d.serveInbox(ctx, conn, r, req)
	case OpHello:
		d.serveExtension(ctx, conn, r, req)
	case OpBoards:
		_ = WriteFrame(conn, d.serveBoards(ctx, req))
	case OpCreateBoard:
		_ = WriteFrame(conn, d.serveCreateBoard(ctx, req))
	case OpJoin:
		_ = WriteFrame(conn, d.serveJoin(ctx, req))
	case OpRegister, OpPrompt, OpTurnStart, OpTurnEnd, OpBoundary, OpUrgent, OpEnd, OpBind, OpAgents:
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
	if req.Op == OpBind {
		release, err := d.joinTurn(ctx, req.Key().String())
		if err != nil {
			return errorResponse("daemon_not_running", "The delivery daemon is stopping.", "Run the command again.")
		}
		defer release()
	}
	return d.callBindingLocked(ctx, req)
}

func (d *Daemon) callBindingLocked(ctx context.Context, req Request) Response {
	ad, ok := d.adapters[req.Harness]
	if !ok || req.Session == "" {
		return errorResponse("invalid_request", fmt.Sprintf("%q is not a harness the delivery daemon knows.", req.Harness),
			"Use "+d.harnessNames()+".")
	}
	if req.Op == OpRegister && req.Launch != "" {
		ticket := req.Launch
		req.Launch = ""
		resp := d.call(ctx, req)
		if resp.Error != nil {
			return resp
		}
		return d.bindLaunch(ctx, req, ticket, resp)
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
	if req.Op == OpBind {
		if r := d.bindingPreflight(ctx, key, *req.Agent); r.Error != nil {
			return r
		}
		var refusal *Response
		req, refusal = d.resolveRequest(ctx, req)
		if refusal != nil {
			return *refusal
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

// bindLaunch binds a session that just registered to the agent its launch ticket names,
// and takes the ticket, so a session started later with the same ticket in its
// environment (one started from inside this one) can't take the seat. resp is the
// register's answer, which then names the agent. A ticket that is gone binds nothing.
func (d *Daemon) bindLaunch(ctx context.Context, req Request, ticket string, resp Response) Response {
	if d.cfg.Tickets == nil {
		return resp
	}
	agent, ok, err := d.cfg.Tickets.Take(ticket)
	if err != nil {
		d.log.Warn("launch ticket: couldn't read it", "session", req.Key().String(), "error", err)
		return resp
	}
	if !ok {
		return resp
	}
	b := d.call(ctx, Request{V: ProtocolVersion, Op: OpBind, Harness: req.Harness, Session: req.Session, Agent: &agent})
	if b.Error != nil {
		return b
	}
	d.log.Info("session took its launched seat", "session", req.Key().String(), "agent", agent.Name, "board", agent.Board)
	resp.Agents, resp.Lost, resp.Previous = b.Agents, nil, b.Previous
	if len(b.Agents) == 1 {
		resp.Mode = d.mode(b.Agents[0])
	}
	return resp
}

// harnessNames lists the harnesses the daemon has adapters for, as "a, b or c".
func (d *Daemon) harnessNames() string {
	names := make([]string, 0, len(d.cfg.Adapters))
	for _, a := range d.cfg.Adapters {
		names = append(names, a.Harness())
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// validationResponse turns an adapter's verdict on a session into an error response,
// in the adapter's own words when it gave them.
func validationResponse(key SessionKey, err error) (Response, bool) {
	var words *SessionError
	said := errors.As(err, &words)
	switch {
	case err == nil:
		return Response{}, false
	case errors.Is(err, ErrSubAgent) && said:
		return errorResponse(ReasonSubAgent, words.Message, words.Hint), true
	case errors.Is(err, ErrSubAgent):
		return errorResponse(ReasonSubAgent,
			"The session "+key.String()+" is a sub-agent, and messages can only go to the root conversation.",
			"Run aboard join or aboard resume in the root conversation instead."), true
	case errors.Is(err, ErrTargetAbsent) && said:
		return errorResponse(ReasonTargetAbsent, words.Message, words.Hint), true
	case errors.Is(err, ErrTargetAbsent):
		return errorResponse(ReasonTargetAbsent,
			"The session "+key.String()+" no longer exists.",
			"Run the command inside that session, or open it again."), true
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
			w.mu.Lock()
			expected := w.pendingHandoff
			w.mu.Unlock()
			if expected != "" && m.HandoffID != expected {
				continue
			}
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
	mu             sync.Mutex
	conn           net.Conn
	received       chan struct{}
	pendingHandoff string
	gone           chan struct{}
}

// Deliver sends the bundle and waits for the hook to say it has it. A hook names no
// delivery, so the id isn't sent.
func (w *waiter) Deliver(ctx context.Context, _ int64, bundle string) error {
	return w.deliverFrame(ctx, Response{V: ProtocolVersion, Event: EventDeliver, Bundle: bundle})
}

func (w *waiter) DeliverHandoff(ctx context.Context, h Handover) error {
	if h.HandoffID == "" {
		return w.Deliver(ctx, h.ID, h.Bundle)
	}
	w.mu.Lock()
	w.pendingHandoff = h.HandoffID
	w.mu.Unlock()
	return w.deliverFrame(ctx, Response{V: ProtocolVersion, Event: EventDeliver, HandoffID: h.HandoffID, DeliveryClass: h.Class, Bundle: h.Bundle})
}

func (w *waiter) deliverFrame(ctx context.Context, frame Response) error {
	err := w.write(frame)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	var stopped error
	select {
	case <-w.received:
		return nil
	case <-w.gone:
		stopped = ErrBusy
	case <-ctx.Done():
		stopped = fmt.Errorf("wait for the hook to take the bundle: %w", ctx.Err())
	}
	// A hook says it has the bundle and exits at once, so it can be both received and
	// gone by now; it has the bundle all the same.
	select {
	case <-w.received:
		return nil
	default:
		return stopped
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
		MultiSeat: multiSeatEnabled,
		PID:       d.cfg.PID, Build: d.cfg.Build, OpenSessions: d.openCount(), Servers: []ServerStatus{},
		Attention: []StatusItem{}, Skipped: []StatusItem{}, Stalled: []StatusItem{}, Agents: []AgentProblem{}, Bindings: []BindingStatus{},
	}
	d.mu.Lock()
	for _, item := range d.stalled {
		st.Stalled = append(st.Stalled, item)
	}
	slices.SortFunc(st.Stalled, func(a, b StatusItem) int { return cmp.Compare(a.ID, b.ID) })
	for url, c := range d.servers {
		connected, problem := c.snapshot()
		st.Servers = append(st.Servers, ServerStatus{URL: url, Connected: connected, Problem: problem})
	}
	for a, reason := range d.problems {
		st.Agents = append(st.Agents, AgentProblem{Agent: d.refs[a], Reason: reason})
	}
	for a, s := range d.owners {
		if d.extensionProblems[s.key] {
			st.Agents = append(st.Agents, AgentProblem{Agent: d.refs[a], Reason: ReasonExtensionOutdated})
		}
		st.Bindings = append(st.Bindings, BindingStatus{Agent: d.refs[a], Session: s.key.String(), Open: d.open[s.key], Turned: d.turned[s.key]})
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

// serveHold keeps replies to one of an agent's messages out of its bundles while the
// connection stays open, and records the messages the command claims on it.
func (d *Daemon) serveHold(ctx context.Context, conn net.Conn, r *bufio.Reader, req Request) {
	if req.Agent == nil || req.ReplyTo <= 0 {
		_ = WriteFrame(conn, errorResponse("invalid_request", "A hold needs an agent and the message whose replies to hold.",
			"Send the agent's server, board and name, and reply_to."))
		return
	}
	s := d.owner(*req.Agent)
	if s == nil {
		_ = WriteFrame(conn, Response{V: ProtocolVersion})
		return
	}
	h := &hold{agent: *req.Agent, replyTo: req.ReplyTo}
	defer s.mail.put(sessionMsg{unhold: h})
	if err := WriteFrame(conn, ask(ctx, s, sessionMsg{hold: h})); err != nil {
		return
	}
	for {
		var m Request
		if err := ReadFrame(r, &m); err != nil {
			return
		}
		if m.Op != OpClaim {
			continue
		}
		if err := WriteFrame(conn, ask(ctx, s, sessionMsg{claim: &claimRequest{agent: h.agent, seqs: m.Seqs}})); err != nil {
			return
		}
	}
}

// ask sends m to the session and returns its answer.
func ask(ctx context.Context, s *session, m sessionMsg) Response {
	reply := make(chan Response, 1)
	m.reply = reply
	s.mail.put(m)
	select {
	case resp := <-reply:
		return resp
	case <-ctx.Done():
		return errorResponse("daemon_not_running", "The delivery daemon is stopping.", "Run the command again.")
	}
}

// serveInbox holds an agent's deliveries and notices while a command reads its inbox,
// so the session can't be handed what the command shows. How far the command then
// acknowledged reaches the daemon from the server, as any client's acknowledgement does.
func (d *Daemon) serveInbox(ctx context.Context, conn net.Conn, r *bufio.Reader, req Request) {
	if req.Agent == nil {
		_ = WriteFrame(conn, errorResponse("invalid_request", "An inbox request needs an agent.", "Send the agent's server, board and name."))
		return
	}
	s := d.owner(*req.Agent)
	if s == nil {
		_ = WriteFrame(conn, Response{V: ProtocolVersion})
		return
	}
	rd := &reading{agent: *req.Agent}
	defer s.mail.put(sessionMsg{doneReading: rd})
	if err := WriteFrame(conn, ask(ctx, s, sessionMsg{req: req, reading: rd})); err != nil {
		return
	}
	// The hold lasts until the command closes the connection; it sends nothing else.
	for {
		var m Request
		if err := ReadFrame(r, &m); err != nil {
			return
		}
	}
}
