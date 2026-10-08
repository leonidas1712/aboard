package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// sessionMsg is one request to a session's goroutine. Exactly one group of fields is set.
type sessionMsg struct {
	// A control request, answered on reply.
	req       Request
	reply     chan<- Response
	preflight *AgentRef
	// waiter is set with an OpWait request.
	waiter *waiter
	// gone reports that a waiting hook's connection closed.
	gone *waiter
	// inbox is the result of fetching an agent's inbox.
	inbox *inboxResult
	// ack is the result of acknowledging an agent's messages.
	ack *ackResult
	// refused is a presence report the server refused for a reason that stops the agent.
	refused *refusal
	// release asks the session to give up an agent, which adopter now owns.
	release *AgentRef
	adopter *session
	// adopt says the previous owner has given the agent up.
	adopt *AgentRef
	// checkAlive asks the session to close if its harness process has gone.
	checkAlive bool
	// modeChanged says an agent's delivery mode changed, so what may be delivered did.
	modeChanged bool
	// modeWas is the mode each changed agent had before, which a session bound before
	// the daemon first read the agent's mode takes as the one it was told.
	modeWas map[AgentKey]Mode
	// credentialChanged refreshes a reused seat after its saved token changed.
	credentialChanged *AgentRef
	// restoreSeat gives back a journal seat that was proved only after the daemon
	// started, because its server couldn't be reached until then.
	restoreSeat *AgentRef
	// renewPresence asks the session to report its agents' presence again.
	renewPresence bool
	// hold starts keeping replies to a message out of bundles, answered on reply;
	// unhold ends it.
	hold, unhold *hold
	// claim records messages a command showed as received, answered on reply.
	claim *claimRequest
	// reading starts holding an agent's deliveries while a command reads its inbox, with
	// the command's request in req, answered on reply; doneReading ends it.
	reading, doneReading *reading
	// read says the server reports an agent's read position moved.
	read *readMove
	// ext is a harness extension's connection, set with its hello in req; from is the
	// connection a later report in req came on; extGone reports a connection closed
	// without a goodbye.
	ext, from, extGone *extConn
}

// hold keeps replies to one of an agent's messages out of its bundles and notices, while
// a command waits for the reply itself.
type hold struct {
	agent   AgentRef
	replyTo int
}

// claimRequest names messages a command showed to the agent.
type claimRequest struct {
	agent AgentRef
	seqs  []int
}

// reading is a command reading an agent's inbox. While it reads, nothing is handed to
// the agent and no notice names its messages, so what the command shows and acknowledges
// can't also reach the session.
type reading struct {
	agent AgentRef
}

// readMove says an agent's read position on its server is now upTo.
type readMove struct {
	agent AgentRef
	upTo  int
}

type inboxResult struct {
	generation uint64
	agent      AgentRef
	msgs       []Message
	cursor     int
	// mode is the agent's delivery mode as its server holds it, read with the messages;
	// nil from a server that doesn't hold modes.
	mode *HeldMode
	err  error
	// refresh is the id of the refresh that asked for it, or zero.
	refresh int64
}

type ackResult struct {
	generation uint64
	agent      AgentRef
	upTo       int
	err        error
}

// session is one open conversation in one harness. Everything below mail is changed
// only by the session's own goroutine, in run.
type session struct {
	handoffs  map[string]*sessionHandoff
	firstSeat int
	d         *Daemon
	key       SessionKey
	adapter   Adapter
	mail      *mailbox[sessionMsg]

	boot string
	open bool
	// started is true once the session has been open, so a register that opens it again
	// is a resume, not a new session.
	started bool
	// proc is the harness process the session runs in, or nil if it isn't known.
	proc *Process
	// lost is the agent another session resumed while this one held it, or nil.
	lost *AgentRef
	// busyAt is when the session last showed it was in a turn: a prompt or a tool call.
	busyAt time.Time
	// inTurn is true while a queueing harness runs a turn, as its prompt and stop hooks
	// report. The owner's messages then wait for a tool hook instead of the queue.
	inTurn bool
	// working is true from a turn's start (a prompt, a tool call, a wake) until its end
	// (the stop hook waiting, or a queueing harness's stop hook), for presence.
	working        bool
	peerTurnActive bool
	peerTurn       uint64
	// reported is the presence and delivery mode last reported for each agent.
	reported map[AgentKey]reportedPresence
	// waiter is the connection waiting while the session is idle: its stop hook's, or
	// its extension's.
	waiter Waiter
	// ext is the harness extension's connection, for a session an extension registered.
	ext *extConn
	// seenTurns is true once the session has reported a turn starting or ending, so the
	// daemon can tell a session that started no turn from one whose turns it can't see
	// (Codex with untrusted hooks).
	seenTurns bool
	// turned is true once the session has run a turn, which is kept in the journal: a
	// harness can resume a session only after it, since only then does it save the
	// conversation.
	turned bool
	// awaitingTurn are deliveries handed to the session while it was idle, waiting for
	// the turn they should start; stallAt is when they count as stalled.
	awaitingTurn []*Delivery
	stallAt      time.Time
	agents       map[AgentKey]*agentState
	// restored is set for sessions loaded from the journal at start.
	restored bool
	// refreshing holds refreshes whose results a waiting hook should see before a
	// bundle is built.
	refreshing  map[int64]bool
	nextRefresh int64
	// gatherUntil is when the next bundle may be handed to a queueing harness.
	gatherUntil time.Time
	// prepareFailures counts the handoffs in a row that failed to prepare.
	prepareFailures int
	// forward holds agents released while still being adopted: once the adoption
	// arrives, it is passed on to the session that took them.
	forward map[AgentKey]*session
	// holds are the replies commands are waiting for themselves.
	holds map[*hold]bool
	// readers are the commands reading an agent's inbox now.
	readers map[*reading]bool
}

// agentState is what a session knows about one of its agents.
type agentState struct {
	midturnPolicy string
	generation    uint64
	ref           AgentRef
	// adopting is true until the previous session has given the agent up.
	adopting bool
	// fetched is true once the inbox has been read at least once.
	fetched bool
	// unread is the agent's unread messages on the server, oldest first.
	unread    []Message
	ackedUpTo int
	acking    bool
	// problem stops deliveries for the agent, such as a revoked token.
	problem    string
	deliveries map[int64]*Delivery
	// announced are the waiting messages a notice has named, so none is named twice.
	announced map[int]bool
	// previewed are the owner's messages too long for a tool boundary that were shown cut
	// short once; they stay unread until the end of the turn or the agent's inbox.
	previewed map[int]bool
	// told is the delivery mode the session was last told the agent has: the mode when
	// it was bound, since the command that bound it says so, or the one a note or a
	// changed-mode line named since. When the agent's mode differs, the session's next
	// turn start or bundle says so. Empty until known: a session bound before the daemon
	// read the agent's mode from its server is taken to know the mode the first read
	// finds, which is the one the binding command read there.
	told Mode
}

func (s *session) now() time.Time { return s.d.cfg.Clock.Now() }

func (s *session) run(ctx context.Context) error {
	if s.restored {
		s.d.setOpen(s.key, s.open)
		s.checkAlive(ctx)
		s.refreshAll(false)
		s.reportPresence(false)
	}
	for {
		var timer <-chan time.Time
		if next := s.nextTimer(); !next.IsZero() {
			timer = s.d.cfg.Clock.After(next.Sub(s.now()))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.mail.ready():
			renew := false
			for _, m := range s.mail.take() {
				renew = renew || m.renewPresence
				s.handle(ctx, m)
			}
			s.tryDeliver(ctx)
			s.reportPresence(renew)
		case <-timer:
			s.checkStalls(ctx)
			s.tryDeliver(ctx)
			s.reportPresence(false)
		}
	}
}

func (s *session) handle(ctx context.Context, m sessionMsg) {
	switch {
	case m.preflight != nil:
		m.reply <- s.preflightBinding(*m.preflight)
	case m.waiter != nil:
		s.onWait(ctx, m.req, m.waiter)
	case m.gone != nil:
		if s.waiter == m.gone {
			s.waiter = nil
		}
	case m.inbox != nil:
		s.onInbox(ctx, *m.inbox)
	case m.ack != nil:
		s.onAck(ctx, *m.ack)
	case m.refused != nil:
		if a, ok := s.agents[m.refused.agent.Key()]; ok && (m.refused.generation == 0 || m.refused.generation == a.generation) {
			s.setProblem(a, problemOf(m.refused.err))
		}
	case m.release != nil:
		s.onRelease(ctx, *m.release, m.adopter)
	case m.adopt != nil:
		s.onAdopt(ctx, *m.adopt)
	case m.checkAlive:
		s.checkAlive(ctx)
	case m.credentialChanged != nil:
		if a := s.agents[m.credentialChanged.Key()]; a != nil && !a.adopting {
			b, err := s.d.cfg.Journal.BindGeneration(ctx, Binding{Agent: a.ref, Session: s.key, BoundAt: s.now(), RetainSiblings: multiSeatEnabled}, true)
			if err != nil {
				s.d.log.Error("advance binding generation", "error", err)
				s.setProblem(a, ReasonUnauthorized)
				break
			}
			a.generation = b.Generation
			a.acking = false
			s.d.setGeneration(a.ref, a.generation)
			for _, dl := range a.deliveries {
				if dl.State == StateHanded {
					s.setState(ctx, dl, StatePending)
				}
			}
			s.refresh(a, s.waiter != nil)
		}
	case m.restoreSeat != nil:
		s.onRestoreSeat(ctx, *m.restoreSeat)
	case m.modeChanged:
		for key, was := range m.modeWas {
			if a := s.agents[key]; a != nil && a.told == "" {
				a.told = was
			}
		}
		s.refreshAll(false)
	case m.renewPresence:
	case m.hold != nil:
		_, ok := s.agents[m.hold.agent.Key()]
		if ok && s.open {
			s.holds[m.hold] = true
		}
		m.reply <- Response{V: ProtocolVersion, Held: ok && s.open}
	case m.unhold != nil:
		delete(s.holds, m.unhold)
		s.queueNow()
	case m.claim != nil:
		m.reply <- Response{V: ProtocolVersion, Claimed: s.onClaim(ctx, *m.claim)}
	case m.reading != nil:
		m.reply <- s.onReading(ctx, m.req, m.reading)
	case m.doneReading != nil:
		delete(s.readers, m.doneReading)
		s.queueNow()
	case m.read != nil:
		s.onRead(ctx, *m.read)
	case m.ext != nil:
		s.onHello(ctx, m.req, m.ext)
	case m.from != nil:
		s.onExtension(ctx, m.req, m.from)
	case m.extGone != nil:
		s.onExtensionGone(ctx, m.extGone)
	default:
		m.reply <- s.onRequest(ctx, m.req)
	}
}

func (s *session) onRequest(ctx context.Context, req Request) Response {
	ok := Response{V: ProtocolVersion}
	if req.Op != OpQueued {
		s.noteProcess(ctx, req)
	}
	switch req.Op {
	case OpPrompt, OpTurnStart, OpBoundary, OpUrgent:
		if s.started && !s.open {
			// A turn runs, so the session is open again, with the agent it still holds: a
			// harness that resumes a session without running its session-start hook (Codex
			// 0.160 resuming a thread) reports in with its first turn instead. The process
			// the turn runs under is the session's now, as a register's would be.
			if req.Process != nil {
				p := *req.Process
				s.proc = &p
			}
			s.setOpen(ctx, true)
			s.d.log.Info("session started", "session", s.key.String(), "source", "turn", "reopened", true,
				"agents", len(s.agents), "lost", false)
		}
	}
	switch req.Op {
	case OpRegister:
		// A session that closed and starts again with the same id (the harness resumed it)
		// is still bound to the agent it filled, unless another session resumed that
		// agent meanwhile; then it starts with none, and the answer names the agent.
		reopened := s.started && !s.open
		s.inTurn, s.working = false, false
		if req.Boot != "" && req.Boot != s.boot {
			s.newBoot(ctx, req.Boot)
		} else {
			s.confirm(ctx)
		}
		s.setOpen(ctx, true)
		ok.Boot, ok.Reopened, ok.Agents = s.boot, reopened, s.agentRefs()
		if len(ok.Agents) == 0 {
			ok.Lost = s.lost
		}
		// The session-start hook prints the note before the session's first turn, which
		// is when the messages that waited arrive.
		ok.Mode, ok.Note = s.startNote(reopened, true)
		ok.Note = s.taskStartNote(ctx, ok.Note)
		s.d.log.Info("session started", "session", s.key.String(), "source", req.Source, "reopened", reopened,
			"agents", len(ok.Agents), "lost", s.lost != nil && len(ok.Agents) == 0)
	case OpPrompt, OpTurnStart:
		s.beginPeerTurn()
		if req.Op == OpTurnStart || req.Harness != "omp" {
			ok.Nudge = s.briefStartNudge(ctx)
		}
		s.busyAt = s.now()
		s.inTurn = !s.adapter.WaitsForIdle()
		s.working = true
		s.turnStarted(ctx)
		s.saveSession(ctx)
		if !req.Wake {
			s.event(ctx, req.Boot)
		}
		if s.waiter != nil {
			s.waiter.Release()
			s.waiter = nil
		}
		if req.Op == OpTurnStart {
			ok.Bundle = s.atTurnStart(ctx)
		}
	case OpBoundary, OpUrgent:
		if slices.Contains(req.Capabilities, "midturn-peer") && req.Boot != "" && s.boot != "" && req.Boot != s.boot {
			return errorResponse("session_unknown", "This tool boundary belongs to an older session boot.", "Keep the message queued for the current session.")
		}
		s.beginPeerTurn()
		s.busyAt = s.now()
		s.working = true
		s.turnStarted(ctx)
		s.saveSession(ctx)
		if req.Boot != "" && req.Boot != s.boot {
			s.newBoot(ctx, req.Boot)
		} else {
			s.confirmBefore(ctx, req.Started)
		}
		ok.Bundle, ok.Notice, ok.HandoffID = s.boundary(ctx, slices.Contains(req.Capabilities, "tool-boundary") && slices.Contains(req.Capabilities, "midturn-peer"))
		if ok.HandoffID != "" {
			ok.Boot, ok.TurnID = s.boot, s.peerTurn
		}
	case OpReceived:
		return s.receivePeerBoundary(ctx, req)
	case OpTurnEnd:
		if req.TurnID != 0 && (req.TurnID != s.peerTurn || req.Boot == "" || req.Boot != s.boot) {
			return ok
		}
		if (!req.Started.IsZero() && req.Started.Before(s.busyAt)) || (req.Boot != "" && req.Boot != s.boot) {
			return ok
		}
		s.markTurned(ctx)
		s.event(ctx, req.Boot)
		s.requeueUnreceivedPeers(ctx)
		s.inTurn, s.working = false, false
		s.peerTurnActive = false
		s.seenTurns = true
		s.saveSession(ctx)
		if len(s.offers(s.queueFilter())) > 0 && s.gatherUntil.IsZero() {
			s.gatherUntil = s.now()
		}
	case OpEnd:
		s.inTurn, s.working = false, false
		s.peerTurnActive = false
		if s.waiter != nil {
			s.waiter.Release()
			s.waiter = nil
		}
		if s.open {
			// Logged like a start, so a session that ends early shows in the log.
			s.d.log.Info("session ended", "session", s.key.String(), "agents", len(s.agents))
		}
		s.setOpen(ctx, false)
	case OpBind:
		if preflight := s.preflightBinding(*req.Agent); preflight.Error != nil {
			return preflight
		}
		var err error
		ok.Previous, err = s.bind(ctx, *req.Agent)
		if err != nil {
			return errorResponse("internal", "Couldn't save the seat binding.", "Look at the daemon log and try again.")
		}
		ok.Agents = s.agentRefs()
		// The command that binds an agent (join, pair, resume, status for a launched
		// session) shows its delivery mode, as the server holds it.
		if a := s.agents[req.Agent.Key()]; a != nil && s.d.heldMode(*req.Agent) {
			a.told = s.d.mode(*req.Agent)
		}
		if !s.open {
			s.setOpen(ctx, true)
		}
	case OpQueued:
		return s.queued(ctx, req)
	case OpAgents:
		ok.Agents = append(ok.Agents, s.agentRefs()...)
		ok.Capabilities = negotiatedCapabilities(s.ext)
	}
	return ok
}

// noteProcess records the harness process a request came from. A register or an
// extension's hello is a harness starting, so it replaces the process; anything else
// only fills it in when it is unknown, so a command run from elsewhere can't keep a dead
// session open.
func (s *session) noteProcess(ctx context.Context, req Request) {
	starting := req.Op == OpRegister || req.Op == OpHello
	if req.Process == nil || (s.proc != nil && (!starting || *s.proc == *req.Process)) {
		return
	}
	p := *req.Process
	s.proc = &p
	s.saveSession(ctx)
}

// checkAlive closes an open session whose harness process has gone, as its end hook
// would have. A harness killed outright never runs that hook.
func (s *session) checkAlive(ctx context.Context) {
	if !s.open || s.proc == nil || s.d.cfg.Processes == nil || s.d.cfg.Processes.Alive(*s.proc) {
		return
	}
	s.d.log.Info("closing session: its harness process has gone", "session", s.key.String(), "pid", s.proc.PID)
	if s.waiter != nil {
		s.waiter.Release()
		s.waiter = nil
	}
	s.working = false
	s.setOpen(ctx, false)
}

// presence is what the session's agent is doing now.
func (s *session) presence() Presence {
	switch {
	case !s.open:
		return PresenceNoSession
	case s.working:
		return PresenceWorking
	default:
		return PresenceIdle
	}
}

// reportedPresence is what was last reported for an agent.
type reportedPresence struct {
	state Presence
	mode  Mode
}

// reportPresence reports the presence and delivery mode of each agent the session holds
// when either changed, or, with renew, again while it isn't no_session, so the server
// doesn't let it run out. no_session isn't renewed: an unrenewed presence becomes it
// anyway.
func (s *session) reportPresence(renew bool) {
	p := s.presence()
	for _, a := range s.agents {
		ref := a.ref
		if a.adopting || a.gone() {
			continue
		}
		now := reportedPresence{state: p, mode: s.d.mode(ref)}
		if last, ok := s.reported[ref.Key()]; ok && last == now && (!renew || p == PresenceNoSession) {
			continue
		}
		s.reported[ref.Key()] = now
		s.d.server(ref.Server).mail.put(srvMsg{presence: &ref, generation: a.generation, state: now.state, mode: now.mode})
	}
}

// leavePresence reports that an agent leaving the session has no session now, unless
// that is already what was reported. The session that takes the agent next reports
// after this, through the same server connection, so its report wins.
func (s *session) leavePresence(ref AgentRef) {
	if last, ok := s.reported[ref.Key()]; ok && last.state != PresenceNoSession {
		s.d.server(ref.Server).mail.put(srvMsg{presence: &ref, generation: s.d.generation(ref), state: PresenceNoSession, mode: last.mode})
	}
	delete(s.reported, ref.Key())
}

func (s *session) agentRefs() []AgentRef {
	refs := make([]AgentRef, 0, len(s.agents))
	for _, a := range s.agents {
		refs = append(refs, a.ref)
	}
	slices.SortFunc(refs, compareAgents)
	return refs
}

// event handles any event from the session: from the same process it confirms what was
// handed over; from a new process it makes unconfirmed bundles go again.
func (s *session) event(ctx context.Context, boot string) {
	if boot != "" && boot != s.boot {
		s.newBoot(ctx, boot)
		return
	}
	s.confirm(ctx)
}

// newBoot records that the session's process changed. Bundles handed to the old process
// were never confirmed, so they are handed again.
func (s *session) newBoot(ctx context.Context, boot string) {
	s.boot = boot
	s.saveSession(ctx)
	s.unhand(ctx)
}

// unhand puts every bundle handed to this session and not confirmed back to pending.
func (s *session) unhand(ctx context.Context) {
	for _, a := range s.agents {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded {
				s.setState(ctx, dl, StatePending)
			}
		}
	}
}

func (s *session) setOpen(ctx context.Context, open bool) {
	if !open {
		// A session that closed starts no turn; what it was handed isn't stalled.
		s.forgetAwaiting()
	}
	s.open = open
	s.started = s.started || open
	s.saveSession(ctx)
	s.d.setOpen(s.key, open)
	for _, a := range s.agents {
		for _, dl := range a.deliveries {
			switch {
			case !open && (dl.State == StateHanded || dl.State == StatePending):
				s.setState(ctx, dl, StateHeld)
			case open && dl.State == StateHeld:
				s.setState(ctx, dl, StatePending)
			}
		}
	}
	if open {
		s.refreshAll(false)
	}
}

// markTurned records that the session has run a turn.
func (s *session) markTurned(ctx context.Context) {
	if s.turned {
		return
	}
	s.turned = true
	s.d.setTurned(s.key)
	s.saveSession(ctx)
}

func (s *session) saveSession(ctx context.Context) {
	rec := SessionRecord{Key: s.key, Boot: s.boot, Open: s.open, Process: s.proc, Lost: s.lost, Turned: s.turned, InTurn: s.inTurn, SeenTurns: s.seenTurns, PeerTurnActive: s.peerTurnActive, PeerTurn: s.peerTurn, BusyAt: s.busyAt, UpdatedAt: s.now()}
	if err := s.d.cfg.Journal.SaveSession(ctx, rec); err != nil {
		s.d.log.Error("save session", "session", s.key.String(), "error", err)
	}
}

func (s *session) beginPeerTurn() {
	if !s.peerTurnActive {
		s.peerTurn++
		s.peerTurnActive = true
	}
}

// confirm marks every bundle handed to this session as received.
func (s *session) confirm(ctx context.Context) { s.confirmBefore(ctx, time.Time{}) }

// confirmBefore marks every bundle handed to this session before t as received, or every
// one when t is zero. A tool hook that started before a bundle was handed to another
// hook running at the same moment (parallel tool calls) doesn't show that the session
// received it.
func (s *session) confirmBefore(ctx context.Context, t time.Time) {
	covered := s.confirmManifests(ctx, t)
	var confirmed []*Delivery
	for _, a := range s.agents {
		for _, dl := range a.deliveries {
			if !covered[dl.ID] && dl.State == StateHanded && dl.Session == s.key && dl.Boot == s.boot && (t.IsZero() || dl.UpdatedAt.Before(t)) {
				s.setState(ctx, dl, StateConfirmed)
				if dl.State == StateConfirmed {
					confirmed = append(confirmed, dl)
				}
			}
		}
		s.maybeAck(a)
	}
	s.logConfirmed(confirmed)
}

func (s *session) setState(ctx context.Context, dl *Delivery, st State) {
	next := *dl
	next.State = st
	next.UpdatedAt = s.now()
	if err := s.d.cfg.Journal.UpdateDelivery(ctx, next); err != nil {
		s.d.log.Error("update delivery", "delivery", dl.ID, "state", st, "error", err)
		return
	}
	*dl = next
}

func (s *session) onWait(ctx context.Context, req Request, w *waiter) {
	s.noteProcess(ctx, req)
	if !req.Started.IsZero() && req.Started.Before(s.busyAt) {
		// The stop hook of a turn that ended before the latest prompt, reaching the daemon
		// late. The session is busy, so the hook is released at once.
		w.accepted()
		w.Release()
		return
	}
	switch {
	case !req.Resumed:
		s.event(ctx, req.Boot)
	case req.Boot != "" && req.Boot != s.boot:
		s.newBoot(ctx, req.Boot)
	default:
		// The hook was waiting when the daemon went away, so a bundle handed to it then
		// never arrived.
		s.unhand(ctx)
	}
	s.requeueUnreceivedPeers(ctx)
	if s.waiter != nil && s.waiter != w {
		s.waiter.Release()
	}
	s.waiter = w
	s.inTurn = false
	s.working = false
	s.seenTurns = true
	s.saveSession(ctx)
	w.acceptedTurn(s.boot, s.peerTurn)
	if !s.open {
		s.setOpen(ctx, true)
	}
	s.refreshAll(true)
}

// bind persists a seat before replacing another seat on the same board.
func (s *session) bind(ctx context.Context, agent AgentRef) (*AgentRef, error) {
	if err := s.ensureBoot(ctx); err != nil {
		return nil, err
	}
	if a, ok := s.agents[agent.Key()]; ok && !a.adopting && a.ref == agent {
		return nil, nil
	}
	binding, err := s.d.cfg.Journal.BindGeneration(ctx, Binding{Agent: agent, Session: s.key, BoundAt: s.now(), RetainSiblings: multiSeatEnabled}, false)
	if err != nil {
		s.d.log.Error("bind agent", "agent", agent.Name, "board", agent.Board, "error", err)
		return nil, err
	}
	if s.lost != nil {
		s.lost = nil
		s.saveSession(ctx)
	}
	var previous *AgentRef
	for _, ref := range s.agentRefs() {
		if ref.Key() != agent.Key() && (!multiSeatEnabled || ref.Board == agent.Board) {
			s.unbind(ctx, ref)
			previous = &ref
		}
	}
	if a, ok := s.agents[agent.Key()]; ok && !a.adopting {
		a.ref = agent
		a.generation = binding.Generation
		s.d.setGeneration(a.ref, a.generation)
		s.d.mu.Lock()
		s.d.rememberLocked(agent)
		s.d.mu.Unlock()
		return previous, nil
	}
	s.agents[agent.Key()] = newAgentState(agent, true)
	s.agents[agent.Key()].generation = binding.Generation
	s.d.setGeneration(agent, binding.Generation)
	if prev := s.d.setOwner(agent, s); prev != nil {
		prev.mail.put(sessionMsg{release: &agent, adopter: s})
		return previous, nil
	}
	s.onAdopt(ctx, agent)
	return previous, nil
}

// onRestoreSeat gives the session back a journal seat, as restore does at start, once
// its token proved it after the daemon started. Nothing happens when the journal no
// longer binds the seat here or another session took it meanwhile.
func (s *session) onRestoreSeat(ctx context.Context, agent AgentRef) {
	if _, ok := s.agents[agent.Key()]; ok || s.d.owner(agent) != nil {
		return
	}
	bindings, err := s.d.cfg.Journal.Bindings(ctx)
	if err != nil {
		s.d.log.Error("restore journal seat: read bindings", "agent", agent.Name, "board", agent.Board, "error", err)
		return
	}
	var binding *Binding
	for i := range bindings {
		if bindings[i].Agent.Key() == agent.Key() && bindings[i].Session == s.key {
			binding = &bindings[i]
		}
	}
	if binding == nil {
		return
	}
	deliveries, err := s.d.cfg.Journal.Deliveries(ctx, openStates...)
	if err != nil {
		s.d.log.Error("restore journal seat: read deliveries", "agent", agent.Name, "board", agent.Board, "error", err)
		return
	}
	a := newAgentState(agent, false)
	a.generation = binding.Generation
	for i := range deliveries {
		if deliveries[i].Agent.Key() == agent.Key() {
			dl := deliveries[i]
			a.deliveries[dl.ID] = &dl
		}
	}
	s.agents[agent.Key()] = a
	s.d.setGeneration(agent, binding.Generation)
	s.d.setProblem(agent, "")
	s.d.setOwner(agent, s)
	s.refresh(a, s.waiter != nil)
}

// unbind lets an agent go with no session to take it. Bundles handed here and never
// confirmed go back to pending, and the agent's unread messages stay unread on its
// server, so whichever session binds the agent next gets both. The journal's binding
// is replaced when the session's new agent is bound.
func (s *session) unbind(ctx context.Context, agent AgentRef) {
	if a := s.agents[agent.Key()]; !a.adopting {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded || dl.State == StateHeld {
				s.setState(ctx, dl, StatePending)
			}
		}
	}
	// An agent still being adopted is given up by its previous session, which puts its
	// bundles back to pending; the adoption, when it arrives, finds no agent here.
	delete(s.agents, agent.Key())
	s.leavePresence(agent)
	s.d.dropOwner(agent, s)
}

// onRelease gives an agent up to the session that bound it. Bundles this session never
// confirmed go to the new one.
func (s *session) onRelease(ctx context.Context, agent AgentRef, to *session) {
	a, ok := s.agents[agent.Key()]
	if ok && a.adopting {
		s.forward[agent.Key()] = to
		return
	}
	if ok {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded || dl.State == StateHeld {
				s.setState(ctx, dl, StatePending)
			}
		}
		delete(s.agents, agent.Key())
		s.leavePresence(agent)
		// Kept so that, if this session starts again, it says the agent went elsewhere
		// rather than silently having none.
		s.lost = &agent
		s.saveSession(ctx)
	}
	to.mail.put(sessionMsg{adopt: &agent})
}

// onAdopt takes over an agent once no other session holds it, loading its deliveries.
func (s *session) onAdopt(ctx context.Context, agent AgentRef) {
	if to, fwd := s.forward[agent.Key()]; fwd {
		delete(s.forward, agent.Key())
		delete(s.agents, agent.Key())
		to.mail.put(sessionMsg{adopt: &agent})
		return
	}
	a, ok := s.agents[agent.Key()]
	if !ok || !a.adopting {
		return
	}
	ds, err := s.d.cfg.Journal.Deliveries(ctx, openStates...)
	if err != nil {
		s.d.log.Error("load deliveries", "agent", agent.Name, "error", err)
	}
	for i := range ds {
		if ds[i].Agent.Key() != agent.Key() {
			continue
		}
		dl := ds[i]
		a.deliveries[dl.ID] = &dl
		switch dl.State {
		case StateHanded, StateHeld:
			s.setState(ctx, &dl, StatePending)
		case StateAttention:
			// Binding the agent again is the fix for a delivery that needed attention.
			dl.Attempts, dl.Reason = 0, ""
			s.setState(ctx, &dl, StatePending)
		case StatePending, StateConfirmed, StateDone, StateRetry, StateSkipped:
		}
	}
	a.adopting = false
	s.refresh(a, s.waiter != nil)
}

// refreshAll asks for every agent's inbox. With gate set, a bundle for a waiting hook is
// built only once the answers are in, so it holds everything sent before the hook came.
func (s *session) refreshAll(gate bool) {
	for _, a := range s.agents {
		if !a.adopting {
			s.refresh(a, gate)
		}
	}
}

func (s *session) refresh(a *agentState, gate bool) {
	if a.gone() {
		return
	}
	s.nextRefresh++
	id := s.nextRefresh
	if gate {
		s.refreshing[id] = true
	}
	s.d.server(a.ref.Server).mail.put(srvMsg{refresh: &a.ref, generation: a.generation, id: id, replyTo: s})
}

func (s *session) onInbox(ctx context.Context, r inboxResult) {
	delete(s.refreshing, r.refresh)
	a, ok := s.agents[r.agent.Key()]
	if !ok || a.gone() || (r.generation != 0 && r.generation != a.generation) {
		return
	}
	if r.err != nil {
		if reason := problemOf(r.err); reason != "" {
			s.setProblem(a, reason)
		}
		s.d.log.Warn("read inbox", "agent", a.ref.Name, "board", a.ref.Board, "error", r.err)
		return
	}
	s.setProblem(a, "")
	a.fetched = true
	a.midturnPolicy = ""
	if r.mode != nil {
		a.midturnPolicy = r.mode.MidturnPolicy
		s.d.learnMode(ctx, a.ref, *r.mode)
	}
	if a.told == "" {
		// The first read shows the mode the binding command saw. The mode in force may
		// already be newer: a change passed on while this read was on its way.
		a.told = s.d.mode(a.ref)
		if r.mode != nil {
			if m, ok := ParseMode(string(r.mode.Mode)); ok {
				a.told = m
			} else {
				a.told = ModeFocused
			}
		}
	}
	if r.cursor > a.ackedUpTo {
		s.movedTo(ctx, a, r.cursor)
	}
	a.unread = a.unread[:0]
	for _, m := range r.msgs {
		if m.Seq > a.ackedUpTo {
			a.unread = append(a.unread, m)
		}
	}
	slices.SortFunc(a.unread, func(x, y Message) int { return cmp.Compare(x.Seq, y.Seq) })
	if s.gatherUntil.IsZero() && len(s.offers(s.queueFilter())) > 0 {
		// The first message that would wake the session: wait for more, so messages close
		// together wake it once.
		s.gatherUntil = s.now().Add(QueueGather)
	}
	s.maybeAck(a)
}

// problemOf returns the problem a failed inbox read or acknowledgement gives the agent,
// or "" for a failure that may pass.
func problemOf(err error) string {
	switch {
	case errors.Is(err, ErrBoardGone):
		return ReasonBoardGone
	case errors.Is(err, ErrUnauthorized):
		return ReasonUnauthorized
	}
	return ""
}

// gone reports whether the agent can't reach its board any more. That is final for the
// agent, so nothing more is read, acknowledged or reported for it.
func (a *agentState) gone() bool { return a.problem == ReasonBoardGone }

func (s *session) setProblem(a *agentState, reason string) {
	// A queued result predating removal cannot clear or weaken its final refusal.
	if a.gone() && reason != ReasonBoardGone {
		return
	}
	if a.problem != reason {
		a.problem = reason
		s.d.setProblem(a.ref, reason)
	}
	if a.gone() {
		// Nothing is reported for the agent from now on, not even no_session when it
		// leaves the session.
		delete(s.reported, a.ref.Key())
	}
}

// refusal is a server's refusal of a request for an agent.
type refusal struct {
	generation uint64
	agent      AgentRef
	err        error
}

// movedTo records that the agent's read position is at cursor: deliveries entirely
// behind it are done, whoever acknowledged them, and messages behind it leave the
// announced and previewed sets.
func (s *session) movedTo(ctx context.Context, a *agentState, cursor int) {
	a.ackedUpTo = cursor
	maps.DeleteFunc(a.announced, func(seq int, _ bool) bool { return seq <= cursor })
	maps.DeleteFunc(a.previewed, func(seq int, _ bool) bool { return seq <= cursor })
	for id, dl := range a.deliveries {
		if slices.Max(append([]int{0}, dl.Seqs...)) > cursor {
			continue
		}
		if dl.State != StateSkipped && dl.State != StateDone {
			s.setState(ctx, dl, StateDone)
		}
		delete(a.deliveries, id)
	}
}

// taken returns the sequence numbers already in one of the agent's deliveries.
func taken(a *agentState) map[int]State {
	t := map[int]State{}
	for _, dl := range a.deliveries {
		for _, seq := range dl.Seqs {
			t[seq] = dl.State
		}
	}
	return t
}

// filter picks which new messages a bundle may carry.
type filter int

const (
	allMessages filter = iota
	// ownerOnly is what a tool boundary in a busy turn may carry: only the agent's
	// owner reaches it mid-turn.
	ownerOnly
	notOwner
	// turnStart is what a turn's start carries for an agent in focused mode: everything
	// still waiting for it, quiet messages included.
	turnStart
)

// fromOwner reports whether the agent's owner sent m. Only the owner's messages reach a
// busy session; a peer's, another person's or another person's agent's wait for the
// end of the turn, urgent or not.
func fromOwner(m Message) bool { return m.Sender == senderOwner }

// senderOwner is the sender label of a message from the reader's owner.
const senderOwner = "owner"

func (f filter) allows(m Message) bool {
	switch f {
	case ownerOnly:
		return fromOwner(m)
	case notOwner:
		return !fromOwner(m)
	case allMessages, turnStart:
	}
	return true
}

// queueFilter is what may go into a queueing harness's queue now: during a turn, the
// owner's messages wait for a tool hook, since the queue would hold them until the turn
// ends.
func (s *session) queueFilter() filter {
	if s.inTurn {
		return notOwner
	}
	return allMessages
}

// held reports whether a command is waiting itself for m, a reply it asked for.
func (s *session) held(agent AgentRef, m Message) bool {
	for h := range s.holds {
		if h.agent.Key() == agent.Key() && m.ReplyToSeq == h.replyTo {
			return true
		}
	}
	return false
}

// onClaim records messages a command showed to the agent as received, as if a session
// had confirmed them, so they are acknowledged in turn and never handed over. A message
// already in a delivery is left as it is.
func (s *session) onClaim(ctx context.Context, c claimRequest) []int {
	a, ok := s.agents[c.agent.Key()]
	if !ok || a.adopting {
		return nil
	}
	t := taken(a)
	var seqs []int
	for _, seq := range c.seqs {
		if _, in := t[seq]; !in && seq > a.ackedUpTo {
			seqs = append(seqs, seq)
		}
	}
	if len(seqs) == 0 {
		return nil
	}
	now := s.now()
	dl := &Delivery{Agent: c.agent, Session: s.key, Boot: s.boot, State: StateConfirmed, Seqs: seqs, CreatedAt: now, UpdatedAt: now}
	id, err := s.d.cfg.Journal.AddDelivery(ctx, *dl)
	if err != nil {
		s.d.log.Error("record claimed messages", "agent", c.agent.Name, "error", err)
		return nil
	}
	dl.ID = id
	a.deliveries[id] = dl
	s.maybeAck(a)
	s.refresh(a, false)
	s.d.log.Info("claimed by a command", "session", s.key.String(), "seqs", seqs)
	return seqs
}

// queueNow lets the session be handed what is waiting at once, after something that kept
// it back ended.
func (s *session) queueNow() {
	if s.gatherUntil.IsZero() && len(s.offers(s.queueFilter())) > 0 {
		s.gatherUntil = s.now()
	}
}

// onReading starts holding an agent's deliveries and notices while a command reads its
// inbox. A command the agent runs in this session, started after a bundle was handed
// here, is the session's next event: the woken turn runs, so the bundle was received.
// The answer lists what the session has received past the read position, which the
// command leaves out: the server still counts it unread until the daemon's
// acknowledgement lands.
func (s *session) onReading(ctx context.Context, req Request, rd *reading) Response {
	a, ok := s.agents[rd.agent.Key()]
	if !ok || a.adopting {
		return Response{V: ProtocolVersion}
	}
	s.readers[rd] = true
	if req.Session != "" && req.Key() == s.key && s.open && !req.Started.IsZero() && (req.Boot == "" || req.Boot == s.boot) {
		s.confirmBefore(ctx, req.Started)
	}
	return Response{V: ProtocolVersion, Held: true, Received: s.received(a)}
}

// received returns the agent's messages past its read position that a session
// confirmed, or a command claimed, but that aren't acknowledged yet.
func (s *session) received(a *agentState) []int {
	var seqs []int
	for _, dl := range a.deliveries {
		if dl.State != StateConfirmed {
			continue
		}
		for _, seq := range dl.Seqs {
			if seq > a.ackedUpTo {
				seqs = append(seqs, seq)
			}
		}
	}
	slices.Sort(seqs)
	return seqs
}

// onRead follows the agent's read position on its server, the authority on what the
// agent has read, whichever client acknowledged: a delivery of messages at or below it,
// even one handed and not yet confirmed, is done, and none of them is handed or named in
// a notice again.
func (s *session) onRead(ctx context.Context, r readMove) {
	a, ok := s.agents[r.agent.Key()]
	if !ok || a.adopting || r.upTo <= a.ackedUpTo {
		return
	}
	s.movedTo(ctx, a, r.upTo)
	kept := a.unread[:0]
	for _, m := range a.unread {
		if m.Seq > r.upTo {
			kept = append(kept, m)
		}
	}
	a.unread = kept
	s.d.log.Info("read position moved", "session", s.key.String(), "agent", a.ref.Name, "board", a.ref.Board, "up_to", r.upTo)
	s.maybeAck(a)
}

// beingRead reports whether a command is reading the agent's inbox now.
func (s *session) beingRead(agent AgentRef) bool {
	for rd := range s.readers {
		if rd.agent.Key() == agent.Key() {
			return true
		}
	}
	return false
}

// newMessages returns unread messages that are in no delivery yet.
func (s *session) newMessages(a *agentState, f filter) []Message {
	t := taken(a)
	var out []Message
	for _, m := range a.unread {
		if _, in := t[m.Seq]; in || !f.allows(m) || s.held(a.ref, m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// maybeAck acknowledges up to the last message that, with every unread message before
// it, is confirmed or skipped. Read positions only move past what the session received.
func (s *session) maybeAck(a *agentState) {
	if a.acking || a.problem != "" || a.adopting {
		return
	}
	t := taken(a)
	point := a.ackedUpTo
	for _, m := range a.unread {
		st, in := t[m.Seq]
		if !in || (st != StateConfirmed && st != StateSkipped) {
			break
		}
		point = m.Seq
	}
	if point > a.ackedUpTo {
		a.acking = true
		s.d.server(a.ref.Server).mail.put(srvMsg{ack: &a.ref, generation: a.generation, upTo: point, replyTo: s})
	}
}

func (s *session) onAck(ctx context.Context, r ackResult) {
	a, ok := s.agents[r.agent.Key()]
	if !ok || a.gone() || (r.generation != 0 && r.generation != a.generation) {
		return
	}
	a.acking = false
	if r.err != nil {
		if reason := problemOf(r.err); reason != "" {
			s.setProblem(a, reason)
		}
		s.d.log.Warn("acknowledge", "agent", a.ref.Name, "board", a.ref.Board, "up_to", r.upTo, "error", r.err)
		return
	}
	if r.upTo > a.ackedUpTo {
		s.movedTo(ctx, a, r.upTo)
		kept := a.unread[:0]
		for _, m := range a.unread {
			if m.Seq > r.upTo {
				kept = append(kept, m)
			}
		}
		a.unread = kept
	}
	s.maybeAck(a)
	if len(a.unread) == 0 {
		// The inbox returns a page at a time; read on in case more is waiting.
		s.refresh(a, false)
	}
}

// offers returns, per agent, what the next bundle should carry: a delivery to hand
// again, or new messages that pass f and the agent's delivery mode. An agent with a
// delivery waiting for a retry or for attention gets nothing, so its messages stay in
// order. In focused mode a bundle that would wake the session carries something only
// when a message in it concerns the agent, and then it carries every message waiting,
// a delivery handed again and new messages together; a turn's start (turnStart) carries
// them all, and is given nothing in any other mode.
func (s *session) offers(f filter) []offer {
	now := s.now()
	var out []offer
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		mode := s.d.mode(ref)
		if a.adopting || !a.fetched || a.problem != "" || mode == ModeOff || s.beingRead(ref) {
			continue
		}
		if f == turnStart && mode != ModeFocused {
			continue
		}
		var again *Delivery
		blocked := false
		for _, id := range slices.Sorted(maps.Keys(a.deliveries)) {
			dl := a.deliveries[id]
			switch dl.State {
			case StateAttention:
				blocked = true
			case StateRetry:
				if dl.RetryAt.After(now) {
					blocked = true
				} else if again == nil {
					again = dl
				}
			case StatePending, StateHeld:
				if again == nil {
					again = dl
				}
			case StateHanded, StateConfirmed, StateDone, StateSkipped:
			}
		}
		if blocked {
			continue
		}
		if f == ownerOnly {
			// Only the owner's new messages go mid-turn, in full.
			if msgs := s.newMessages(a, f); again == nil && len(msgs) > 0 {
				orderForBundle(msgs)
				out = append(out, offer{agent: ref, mode: ModeAll, msgs: msgs})
			}
			continue
		}
		var agentOffers []offer
		if again != nil {
			if msgs := s.messagesFor(a, again.Seqs); len(msgs) > 0 {
				orderForBundle(msgs)
				agentOffers = append(agentOffers, offer{agent: ref, mode: mode, redeliver: again.ID, msgs: msgs})
			}
		}
		// A delivery handed again goes on its own, as it went before, unless it holds only
		// messages that wouldn't wake the agent now (quiet ones in focused mode, agents'
		// in humans mode, as when the mode changed after it was made), which wait with
		// the new ones for a message that wakes the agent, or, in focused mode, for its
		// next turn's start, which takes everything.
		if again == nil || (mode == ModeFocused && (f == turnStart || !anyConcerns(agentOffers, ref.Name))) ||
			(mode == ModeHumans && !anyFromHuman(agentOffers)) {
			msgs := s.newMessages(a, f)
			if len(msgs) > 0 {
				orderForBundle(msgs)
				agentOffers = append(agentOffers, offer{agent: ref, mode: mode, msgs: msgs})
			}
		}
		if mode == ModeFocused && f != turnStart && !anyConcerns(agentOffers, ref.Name) {
			// Quiet messages never start a turn; they wait for the agent's next one.
			continue
		}
		if mode == ModeHumans && !anyFromHuman(agentOffers) {
			// Agents' messages wait for a person's, which brings them all.
			continue
		}
		out = append(out, agentOffers...)
	}
	return out
}

// anyFromHuman reports whether a person sent a message in offers. An agent in humans
// mode wakes only for one; its bundle then carries every message waiting, peer ones too.
// Mid-turn only the owner's messages go, and the owner is a person.
func anyFromHuman(offers []offer) bool {
	for _, o := range offers {
		if slices.ContainsFunc(o.msgs, func(m Message) bool { return m.FromHuman }) {
			return true
		}
	}
	return false
}

// anyConcerns reports whether a message in offers concerns the agent called name.
func anyConcerns(offers []offer, name string) bool {
	for _, o := range offers {
		for _, m := range o.msgs {
			if Concerns(m, name) {
				return true
			}
		}
	}
	return false
}

// messagesFor returns the unread messages with the given sequence numbers.
func (s *session) messagesFor(a *agentState, seqs []int) []Message {
	var out []Message
	for _, m := range a.unread {
		if slices.Contains(seqs, m.Seq) {
			out = append(out, m)
		}
	}
	return out
}

// earliestRetry returns when the soonest retry is due, or the zero time.
func (s *session) earliestRetry() time.Time {
	var t time.Time
	for _, a := range s.agents {
		for _, dl := range a.deliveries {
			if dl.State == StateRetry && (t.IsZero() || dl.RetryAt.Before(t)) {
				t = dl.RetryAt
			}
		}
	}
	return t
}

// tryDeliver hands the next bundle over if the session can take one now.
func (s *session) tryDeliver(ctx context.Context) {
	if !s.open || len(s.refreshing) > 0 || !s.canTake() {
		return
	}
	if s.gatherUntil.IsZero() || s.now().Before(s.gatherUntil) {
		return
	}
	s.gatherUntil = time.Time{}
	offers := s.offers(s.queueFilter())
	if len(offers) > 0 {
		// The server is the authority on what the agent has read: check just before
		// handing, in case a read the stream reports hasn't arrived yet.
		if !s.recheck(ctx) {
			s.gatherUntil = s.now().Add(QueueGather)
			return
		}
		offers = s.offers(s.queueFilter())
	}
	notes, told := s.modeNotes()
	limit := BundleLimit
	stopHand := s.key.Harness == "codex" && s.waiter != nil
	if stopHand {
		limit = codexStopLimit
	}
	if notes != "" {
		limit -= len(notes) + 1
	}
	c := s.composePending(offers, limit, s.compositionOptions(BundleLimit), notes)
	if !stopHand {
		s.skip(ctx, c.tooLarge)
	}
	if len(c.parts) == 0 {
		if stopHand {
			s.stopReadHint(ctx, offers)
		}
		s.scheduleRetry()
		return
	}
	c.text = withNotes(notes, c.text)
	handoff, handed, prepareErr := s.prepare(ctx, c, notes, c.text)
	if prepareErr != nil {
		s.gatherUntil = s.now().Add(s.prepareFailed("prepare handoff", prepareErr))
		return
	}
	s.prepared()
	var first int64
	if len(handed) > 0 {
		first = handed[0].ID
	}
	began := s.now()
	hookHand := s.waiter != nil
	idle := !hookHand && (s.adapter.WaitsForIdle() || !s.inTurn)
	confirmed, err := s.hand(ctx, Handover{SessionID: s.key.ID, ID: first, HandoffID: func() string {
		if len(s.agents) > 1 {
			return handoff.manifest.ID
		}
		return ""
	}(), Class: handoff.manifest.Class, Bundle: c.text, Waiter: s.waiter})
	if err == nil {
		s.accepted(ctx, handed, idle)
		s.markTold(told)
	}
	if s.adapter.WaitsForIdle() || hookHand {
		s.waiter = nil // a waiting hook takes one bundle, or has gone
	}
	if err == nil && (s.adapter.WaitsForIdle() || hookHand) {
		s.inTurn = !s.adapter.WaitsForIdle()
		s.working = true // the waiting hook took the bundle and wakes the session with it
		s.saveSession(ctx)
	}
	switch {
	case err == nil && confirmed:
		confirmedParts, confirmErr := s.confirmHandoff(ctx, handoff)
		if confirmErr != nil {
			s.d.log.Error("confirm handoff", "error", confirmErr)
		} else {
			s.logConfirmed(confirmedParts)
		}
	case err == nil:
	case errors.Is(err, ErrBusy):
		for _, dl := range handed {
			s.setState(ctx, dl, StatePending)
		}
		if !s.adapter.WaitsForIdle() {
			// Busy is never a failed attempt; a queueing harness is simply asked again.
			s.gatherUntil = s.now().Add(QueueGather)
		}
	default:
		s.failed(ctx, handed, err)
	}
	for _, a := range s.agents {
		s.maybeAck(a)
	}
	if s.gatherUntil.IsZero() && len(s.offers(s.queueFilter())) > 0 {
		// What didn't fit, or came while handing, has waited already.
		s.gatherUntil = s.now()
	}
	s.scheduleRetry()
	s.d.log.Info("bundle handed", "session", s.key.String(), "board", partBoard(c.parts), "deliveries", len(handed), "seqs", partSeqs(c.parts),
		"bytes", len(c.text), "began", began, "error", errText(err))
}

const codexStopLimit = 2000

// stopReadHint leaves bodies pending when they cannot fit a continuation reason.
func (s *session) stopReadHint(ctx context.Context, offers []offer) {
	text := "Aboard: queued messages need a full read. Run aboard inbox to read and acknowledge them. These hints are not receipts.\n"
	var shown []offer
	for _, o := range offers {
		a := s.agents[o.agent.Key()]
		for _, m := range o.msgs {
			if a.previewed[m.Seq] {
				continue
			}
			line := fmt.Sprintf("Board %q: #%d from @%s.\n", a.ref.Board, m.Seq, m.FromName)
			if len(text)+len(line) > codexStopLimit {
				break
			}
			text += line
			shown = append(shown, offer{agent: o.agent, msgs: []Message{m}})
		}
	}
	if len(shown) == 0 {
		return
	}
	if _, err := s.hand(ctx, Handover{SessionID: s.key.ID, Bundle: text, Waiter: s.waiter}); err != nil {
		return
	}
	for _, o := range shown {
		s.agents[o.agent.Key()].previewed[o.msgs[0].Seq] = true
	}
	s.waiter = nil
	s.inTurn, s.working = true, true
	s.saveSession(ctx)
}

// accepted records that the harness took the deliveries of a bundle. Handed to an idle
// session whose turns the daemon sees, they wait for the turn they should start, and
// count as stalled after StallAfter without one.
func (s *session) accepted(ctx context.Context, ds []*Delivery, idle bool) {
	now := s.now()
	for _, dl := range ds {
		dl.AcceptedAt = now
		if !idle {
			dl.TurnStartedAt = now
		}
		s.saveDelivery(ctx, dl)
	}
	if !idle || !s.seenTurns || len(ds) == 0 {
		return
	}
	s.awaitingTurn = append(s.awaitingTurn, ds...)
	if s.stallAt.IsZero() {
		s.stallAt = now.Add(StallAfter)
	}
}

// turnStarted records that a turn started: every delivery waiting for one has it, and
// one that had stalled no longer has.
func (s *session) turnStarted(ctx context.Context) {
	s.seenTurns = true
	s.markTurned(ctx)
	now := s.now()
	for _, dl := range s.awaitingTurn {
		dl.TurnStartedAt = now
		if dl.Stalled {
			dl.Stalled = false
			s.d.setStalled(dl, false)
			s.d.log.Info("stalled delivery's turn started", "session", s.key.String(), "delivery", dl.ID, "seqs", dl.Seqs)
		}
		s.saveDelivery(ctx, dl)
	}
	s.awaitingTurn, s.stallAt = nil, time.Time{}
}

// checkStalls marks the deliveries still waiting for a turn once StallAfter has passed.
// A stalled delivery is reported, never handed again because of it: the harness has it.
func (s *session) checkStalls(ctx context.Context) {
	if s.stallAt.IsZero() || s.now().Before(s.stallAt) {
		return
	}
	s.stallAt = time.Time{}
	for _, dl := range s.awaitingTurn {
		if dl.Stalled || !dl.TurnStartedAt.IsZero() {
			continue
		}
		dl.Stalled = true
		s.saveDelivery(ctx, dl)
		s.d.setStalled(dl, true)
		s.d.log.Warn("delivery stalled: the session started no turn", "session", s.key.String(), "delivery", dl.ID,
			"seqs", dl.Seqs, "after", StallAfter)
	}
}

// forgetAwaiting drops the deliveries waiting for a turn, and their stalls.
func (s *session) forgetAwaiting() {
	for _, dl := range s.awaitingTurn {
		if dl.Stalled {
			s.d.setStalled(dl, false)
		}
	}
	s.awaitingTurn, s.stallAt = nil, time.Time{}
}

// canTake keeps a tracked busy turn's backlog in the daemon, where reads can remove
// it and turn end can coalesce it. An accepted idle wake waits for its turn to start.
func (s *session) canTake() bool {
	if s.adapter.WaitsForIdle() {
		return s.waiter != nil
	}
	return s.waiter != nil || (!s.inTurn && len(s.awaitingTurn) == 0)
}

// nextTimer is when the session next has something to do on its own: hand a bundle once
// its messages are gathered, or check for stalls; zero for nothing.
func (s *session) nextTimer() time.Time {
	gather := s.gatherUntil
	if !s.canTake() {
		// Nothing can be handed until a waiter comes, which runs the session anyway.
		gather = time.Time{}
	}
	switch {
	case gather.IsZero():
		return s.stallAt
	case s.stallAt.IsZero() || gather.Before(s.stallAt):
		return gather
	}
	return s.stallAt
}

// saveDelivery writes a delivery to the journal as it is, without moving its state.
func (s *session) saveDelivery(ctx context.Context, dl *Delivery) {
	if err := s.d.cfg.Journal.UpdateDelivery(ctx, *dl); err != nil {
		s.d.log.Error("update delivery", "delivery", dl.ID, "error", err)
	}
}

// partBoard is the board of a bundle's messages, for the log: a session holds one agent,
// so one board.
func partBoard(parts []offer) string {
	if len(parts) == 0 {
		return ""
	}
	return parts[0].agent.Board
}

// logConfirmed logs deliveries the session just confirmed, so the time a harness took to
// take a bundle can be read from the log.
func (s *session) logConfirmed(ds []*Delivery) {
	var seqs []int
	board := ""
	for _, dl := range ds {
		seqs = append(seqs, dl.Seqs...)
		board = dl.Agent.Board
	}
	if len(seqs) > 0 {
		s.d.log.Info("bundle confirmed", "session", s.key.String(), "board", board, "seqs", seqs)
	}
}

// hand calls the harness. The call isn't cut off the moment the daemon starts stopping:
// it may run for ShutdownGrace more, so a bundle is rarely cut off half handed over.
func (s *session) hand(ctx context.Context, h Handover) (bool, error) {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), harnessCallTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() {
		select {
		case <-s.d.cfg.Clock.After(ShutdownGrace):
			cancel()
		case <-hctx.Done():
		}
	})
	defer stop()
	if s.waiter != nil && !s.adapter.WaitsForIdle() {
		if w, ok := s.waiter.(HandoffWaiter); ok {
			return false, w.DeliverHandoff(hctx, h)
		}
		if h.HandoffID != "" {
			return false, ErrExtensionOutdated
		}
		return false, s.waiter.Deliver(hctx, h.ID, h.Bundle)
	}
	return s.adapter.Hand(hctx, h)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// scheduleRetry wakes a queueing session when its next retry is due.
func (s *session) scheduleRetry() {
	if s.adapter.WaitsForIdle() {
		return
	}
	if t := s.earliestRetry(); !t.IsZero() && (s.gatherUntil.IsZero() || t.Before(s.gatherUntil)) {
		s.gatherUntil = t
	}
}

// failed counts a failed attempt for each delivery, backing off and stopping for
// attention after MaxAttempts.
func (s *session) failed(ctx context.Context, ds []*Delivery, err error) {
	reason := ReasonHarnessError
	switch {
	case errors.Is(err, ErrTargetAbsent):
		reason = ReasonTargetAbsent
	case errors.Is(err, ErrSubAgent):
		reason = ReasonSubAgent
	}
	for _, dl := range ds {
		dl.Attempts++
		dl.Reason = reason
		if dl.Attempts >= MaxAttempts {
			s.setState(ctx, dl, StateAttention)
			continue
		}
		dl.RetryAt = s.now().Add(backoff(dl.Attempts))
		s.setState(ctx, dl, StateRetry)
	}
	s.d.log.Warn("delivery failed", "session", s.key.String(), "reason", reason, "error", err)
}

// record writes the deliveries for a bundle in state st before it is handed over, so a
// crash after handing it over hands it over again rather than losing it.
func (s *session) record(ctx context.Context, parts []offer, st State) []*Delivery {
	var out []*Delivery
	for _, p := range parts {
		a := s.agents[p.agent.Key()]
		if p.redeliver != 0 {
			dl := a.deliveries[p.redeliver]
			dl.Session, dl.Boot = s.key, s.boot
			s.setState(ctx, dl, st)
			out = append(out, dl)
			continue
		}
		now := s.now()
		dl := &Delivery{Agent: p.agent, Session: s.key, Boot: s.boot, State: st, Seqs: seqsOf(p.msgs), CreatedAt: now, UpdatedAt: now}
		id, err := s.d.cfg.Journal.AddDelivery(ctx, *dl)
		if err != nil {
			s.d.log.Error("record delivery", "agent", p.agent.Name, "error", err)
			continue
		}
		dl.ID = id
		a.deliveries[id] = dl
		out = append(out, dl)
	}
	return out
}

// skip records messages too large for any bundle, so the read position moves past them.
func (s *session) skip(ctx context.Context, parts []offer) {
	for _, dl := range s.record(ctx, parts, StateSkipped) {
		dl.Reason = ReasonTooLarge
		s.setState(ctx, dl, StateSkipped)
		s.d.log.Warn("message too large to deliver", "agent", dl.Agent.Name, "board", dl.Agent.Board, "seqs", dl.Seqs)
	}
	for _, p := range parts {
		s.maybeAck(s.agents[p.agent.Key()])
	}
}

// MidTurnLimit is the most text, in bytes, one tool boundary adds to a busy turn.
// Claude Code takes at most 10,000 characters of context from a hook and moves anything
// longer to a file, so the limit stays under that with room for the notice.
const MidTurnLimit = 9000

// previewLimit is how much of a message too long for a tool boundary is shown.
const previewLimit = 2000

// midTurnFrame is Aboard's line before the owner's messages handed mid-turn.
const midTurnFrame = "Aboard: your owner sent this while you were working; the text inside the tags is theirs.\n"

const peerMidTurnFrame = "Aboard: urgent message from another agent of your owner at this step; the text inside the tags is theirs.\n"

// boundary answers a busy session's tool hook: the owner's waiting messages, handed
// over, and a notice naming other messages that arrived since the last notice. Each
// message is claimed by one hook, since the session answers one request at a time.
func (s *session) boundary(ctx context.Context, peers bool) (bundle, notice, handoffID string) {
	if !s.open {
		return "", "", ""
	}
	if s.key.Harness == "omp" {
		peers = peers && s.ext.SupportsHandoffs() && s.ext.supportsPeer
	}
	if (peers || len(s.offers(ownerOnly)) > 0 || s.anyToAnnounce()) && !s.recheck(ctx) {
		return "", "", ""
	}
	prefix := midTurnFrame
	offers := s.peerBoundaryOffers(peers)
	ownerMessages, peerMessages := false, false
	for _, offer := range offers {
		for _, m := range offer.msgs {
			if m.PeerBoundary {
				peerMessages = true
			} else {
				ownerMessages = true
			}
		}
	}
	if peerMessages {
		prefix = peerMidTurnFrame
		if ownerMessages {
			prefix = midTurnFrame + prefix
		}
	}
	c := s.composePending(offers, MidTurnLimit-len(prefix), s.compositionOptions(MidTurnLimit), prefix)
	for _, part := range c.parts {
		for _, m := range part.msgs {
			c.peerBoundary = c.peerBoundary || m.PeerBoundary
		}
	}
	text := c.text
	previews := map[AgentKey][]int{}
	for _, o := range c.tooLarge {
		a := s.agents[o.agent.Key()]
		m := o.msgs[0]
		if m.PeerBoundary {
			continue
		}
		if a.previewed[m.Seq] {
			continue
		}
		preview := previewTextFor(m, s.textContext(a.ref))
		if len(text)+len(preview)+len(prefix)+2 > MidTurnLimit {
			break
		}
		previews[a.ref.Key()] = append(previews[a.ref.Key()], m.Seq)
		text = strings.TrimSpace(text + "\n\n" + preview)
	}
	if text != "" {
		bundle = prefix + text
	}
	announcements := map[AgentKey][]int{}
	var announced []int
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		if n, seqs := s.noticeFor(a); n != "" {
			candidate := notice + n
			total := len(bundle) + len(candidate)
			if bundle != "" {
				total += 2
			}
			if total > MidTurnLimit {
				continue
			}
			notice = candidate
			announcements[a.ref.Key()] = seqs
			announced = append(announced, seqs...)
		}
	}
	if len(c.parts) > 0 {
		payload := bundle
		if notice != "" {
			payload = bundle + "\n\n" + notice
		}
		h, deliveries, err := s.prepare(ctx, c, prefix, payload)
		if err != nil {
			s.prepareFailed("prepare boundary handoff", err)
			return "", "", ""
		}
		if c.peerBoundary {
			handoffID = h.manifest.ID
		}
		s.prepared()
		now := s.now()
		for _, dl := range deliveries {
			dl.AcceptedAt, dl.TurnStartedAt = now, now
			s.saveDelivery(ctx, dl)
		}
	}
	for key, seqs := range previews {
		for _, seq := range seqs {
			s.agents[key].previewed[seq] = true
		}
	}
	for key, seqs := range announcements {
		for _, seq := range seqs {
			s.agents[key].announced[seq] = true
		}
	}
	if bundle != "" || notice != "" {
		s.d.log.Info("tool boundary", "session", s.key.String(), "seqs", partSeqs(c.parts), "announced", announced, "bytes", len(bundle)+len(notice))
	}
	return bundle, notice, handoffID
}

// atTurnStart answers a turn's start: in focused mode, every message still waiting for
// the agent, quiet ones included, under MidTurnLimit bytes. They are handed into a turn
// that is starting, so the session's next event confirms them, as it does a bundle. A
// message too large for here waits for the next bundle.
func (s *session) atTurnStart(ctx context.Context) string {
	if !s.open {
		return ""
	}
	notes, told := s.modeNotes()
	s.markTold(told)
	if len(s.offers(turnStart)) == 0 {
		return notes
	}
	// The server is the authority on what the agent has read.
	if !s.recheck(ctx) {
		return notes
	}
	limit := MidTurnLimit
	if notes != "" {
		limit -= len(notes) + 1
	}
	c := s.composePending(s.offers(turnStart), limit, s.compositionOptions(MidTurnLimit), notes)
	if len(c.parts) == 0 {
		return notes
	}
	_, deliveries, err := s.prepare(ctx, c, notes, withNotes(notes, c.text))
	if err != nil {
		s.prepareFailed("prepare turn-start handoff", err)
		return notes
	}
	s.prepared()
	now := s.now()
	for _, dl := range deliveries {
		dl.AcceptedAt, dl.TurnStartedAt = now, now
		s.saveDelivery(ctx, dl)
	}
	s.d.log.Info("turn start", "session", s.key.String(), "board", partBoard(c.parts), "seqs", partSeqs(c.parts), "bytes", len(c.text))
	return withNotes(notes, c.text)
}

// modeNotes writes a line for each of the session's agents whose delivery mode changed
// since the session was last told it, saying the new mode and what it means, and
// returns the modes they tell, for markTold once the text is handed over.
func (s *session) modeNotes() (notes string, told map[AgentRef]Mode) {
	var lines []string
	told = map[AgentRef]Mode{}
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		if a.adopting {
			continue
		}
		if m := s.d.mode(ref); a.told != m {
			if a.told != "" {
				lines = append(lines, deliverytext.ModeChanged(ref.Board, string(a.told), string(m)))
			}
			told[ref] = m
		}
	}
	return strings.Join(lines, "\n"), told
}

// markTold records the modes a turn start or a bundle told the session.
func (s *session) markTold(told map[AgentRef]Mode) {
	for ref, m := range told {
		if a := s.agents[ref.Key()]; a != nil {
			a.told = m
		}
	}
}

// withNotes puts the changed-mode lines before a bundle's text.
func withNotes(notes, text string) string {
	if notes == "" {
		return text
	}
	if text == "" {
		return notes
	}
	return notes + "\n" + text
}

// startNote is what a session that comes back is told as it starts, and the mode of the
// agent it holds: which agent it is again, with its delivery mode and rule, or which
// agent it lost. turnEnd says the messages that waited arrive when the first turn ends.
// The note tells the session its agent's mode.
func (s *session) startNote(reopened, turnEnd bool) (mode Mode, note string) {
	refs := s.agentRefs()
	if len(refs) != 1 {
		if len(refs) == 0 && s.lost != nil {
			return "", deliverytext.Lost(s.lost.Name, s.lost.Board)
		}
		return "", ""
	}
	ref := refs[0]
	mode = s.d.mode(ref)
	if !reopened {
		return mode, ""
	}
	s.markTold(map[AgentRef]Mode{ref: mode})
	return mode, deliverytext.Reopened(ref.Name, ref.Board, string(mode), turnEnd)
}

// partSeqs lists the sequence numbers in a bundle's parts, for the log.
func partSeqs(parts []offer) []int {
	var seqs []int
	for _, p := range parts {
		seqs = append(seqs, seqsOf(p.msgs)...)
	}
	return seqs
}

// previewText shows the start of a message too long for a tool boundary, and where the
// rest is.
func previewTextFor(m Message, renderContext deliverytext.Context) string {
	cut := m
	cut.Body, cut.Truncated, cut.ExpectsReply = strings.ToValidUTF8(m.Body[:min(previewLimit, len(m.Body))], ""), true, false
	command := "aboard inbox"
	if renderContext.BoardQualified {
		command += " --board " + m.Board
	}
	return deliverytext.Bundle(m.Board, []Message{cut}, renderContext) +
		fmt.Sprintf("\nMessage #%d is longer than fits here; all of it waits in your inbox: run %s to read it now.", m.Seq, command)
}

// noticeFor names the agent's waiting messages, other than its owner's, that no notice
// has named yet; empty when there are none, or the agent's mode delivers nothing.
func (s *session) noticeFor(a *agentState) (notice string, seqs []int) {
	fresh := s.toAnnounce(a)
	if len(fresh) == 0 {
		return "", nil
	}
	return deliverytext.Notice(a.ref.Board, fresh, s.textContext(a.ref)), seqsOf(fresh)
}

// toAnnounce returns the agent's waiting messages, other than its owner's, that no
// notice has named yet; none when the agent's mode delivers nothing.
func (s *session) toAnnounce(a *agentState) []Message {
	if a.adopting || !a.fetched || a.problem != "" || s.d.mode(a.ref) == ModeOff || s.beingRead(a.ref) {
		return nil
	}
	t := taken(a)
	var fresh []Message
	for _, m := range a.unread {
		if _, in := t[m.Seq]; in || fromOwner(m) || a.announced[m.Seq] || s.held(a.ref, m) {
			continue
		}
		fresh = append(fresh, m)
	}
	return fresh
}

// anyToAnnounce reports whether a notice would name anything now.
func (s *session) anyToAnnounce() bool {
	for _, a := range s.agents {
		if len(s.toAnnounce(a)) > 0 {
			return true
		}
	}
	return false
}

// recheckTimeout bounds the read of an agent's inbox just before a hand or a notice. It
// holds up the session, and a tool hook waiting for its answer, so it is short; a read
// that fails goes by what the server's stream reported.
const recheckTimeout = 5 * time.Second

// recheck reads each agent's inbox and read position from its server now, in the
// session's goroutine, so a bundle or a notice never carries a message the agent read
// through any client a moment ago, before the stream's report of it arrived. It costs
// one request per agent, made only when there is something to hand or announce.
func (s *session) recheck(ctx context.Context) bool {
	fresh := true
	for _, ref := range s.agentRefs() {
		if a := s.agents[ref.Key()]; a.adopting || a.gone() {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, recheckTimeout)
		msgs, cursor, mode, err := s.d.server(ref.Server).srv.Inbox(rctx, ref)
		cancel()
		if err != nil && problemOf(err) == "" {
			s.d.log.Warn("recheck inbox", "agent", ref.Name, "board", ref.Board, "error", err)
			fresh = false
			continue
		}
		s.onInbox(ctx, inboxResult{agent: ref, msgs: msgs, cursor: cursor, mode: mode, err: err})
	}
	return fresh
}

func newAgentState(ref AgentRef, adopting bool) *agentState {
	return &agentState{
		ref: ref, adopting: adopting, deliveries: map[int64]*Delivery{},
		announced: map[int]bool{}, previewed: map[int]bool{},
	}
}
