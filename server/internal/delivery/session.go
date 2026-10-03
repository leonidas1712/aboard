package delivery

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"time"
)

// sessionMsg is one request to a session's goroutine. Exactly one group of fields is set.
type sessionMsg struct {
	// A control request, answered on reply.
	req   Request
	reply chan<- Response
	// waiter is set with an OpWait request.
	waiter *waiter
	// gone reports that a waiting hook's connection closed.
	gone *waiter
	// inbox is the result of fetching an agent's inbox.
	inbox *inboxResult
	// ack is the result of acknowledging an agent's messages.
	ack *ackResult
	// release asks the session to give up an agent, which adopter now owns.
	release *AgentRef
	adopter *session
	// adopt says the previous owner has given the agent up.
	adopt *AgentRef
	// checkAlive asks the session to close if its harness process has gone.
	checkAlive bool
	// modeChanged says an agent's delivery mode changed, so what may be delivered did.
	modeChanged bool
	// renewPresence asks the session to report its agents' presence again.
	renewPresence bool
}

type inboxResult struct {
	agent  AgentRef
	msgs   []Message
	cursor int
	err    error
	// refresh is the id of the refresh that asked for it, or zero.
	refresh int64
}

type ackResult struct {
	agent AgentRef
	upTo  int
	err   error
}

// session is one open conversation in one harness. Everything below mail is changed
// only by the session's own goroutine, in run.
type session struct {
	d       *Daemon
	key     SessionKey
	adapter Adapter
	mail    *mailbox[sessionMsg]

	boot string
	open bool
	// proc is the harness process the session runs in, or nil if it isn't known.
	proc *Process
	// busyAt is when the session last showed it was in a turn: a prompt or a tool call.
	busyAt time.Time
	// inTurn is true while a queueing harness runs a turn, as its prompt and stop hooks
	// report. Urgent messages then wait for a tool hook instead of the queue.
	inTurn bool
	// working is true from a turn's start (a prompt, a tool call, a wake) until its end
	// (the stop hook waiting, or a queueing harness's stop hook), for presence.
	working bool
	// reported is the presence last reported for each agent.
	reported map[AgentRef]Presence
	waiter   *waiter
	agents   map[AgentRef]*agentState
	// restored is set for sessions loaded from the journal at start.
	restored bool
	// refreshing holds refreshes whose results a waiting hook should see before a
	// bundle is built.
	refreshing  map[int64]bool
	nextRefresh int64
	// gatherUntil is when the next bundle may be handed to a queueing harness.
	gatherUntil time.Time
	// forward holds agents released while still being adopted: once the adoption
	// arrives, it is passed on to the session that took them.
	forward map[AgentRef]*session
}

// agentState is what a session knows about one of its agents.
type agentState struct {
	ref AgentRef
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
		if !s.gatherUntil.IsZero() {
			timer = s.d.cfg.Clock.After(s.gatherUntil.Sub(s.now()))
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
			s.tryDeliver(ctx)
		}
	}
}

func (s *session) handle(ctx context.Context, m sessionMsg) {
	switch {
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
	case m.release != nil:
		s.onRelease(ctx, *m.release, m.adopter)
	case m.adopt != nil:
		s.onAdopt(ctx, *m.adopt)
	case m.checkAlive:
		s.checkAlive(ctx)
	case m.modeChanged:
		s.refreshAll(false)
	case m.renewPresence:
	default:
		m.reply <- s.onRequest(ctx, m.req)
	}
}

func (s *session) onRequest(ctx context.Context, req Request) Response {
	ok := Response{V: ProtocolVersion}
	s.noteProcess(ctx, req)
	switch req.Op {
	case OpRegister:
		s.inTurn, s.working = false, false
		if req.Boot != "" && req.Boot != s.boot {
			s.newBoot(ctx, req.Boot)
		} else {
			s.confirm(ctx)
		}
		s.setOpen(ctx, true)
		ok.Boot = s.boot
	case OpPrompt:
		s.busyAt = s.now()
		s.inTurn = !s.adapter.WaitsForIdle()
		s.working = true
		if !req.Wake {
			s.event(ctx, req.Boot)
		}
		if s.waiter != nil {
			s.waiter.Release()
			s.waiter = nil
		}
	case OpUrgent:
		s.busyAt = s.now()
		s.working = true
		s.event(ctx, req.Boot)
		ok.Bundle = s.handUrgent(ctx)
	case OpTurnEnd:
		s.inTurn, s.working = false, false
		if len(s.offers(s.queueFilter())) > 0 && s.gatherUntil.IsZero() {
			s.gatherUntil = s.now()
		}
	case OpEnd:
		s.inTurn, s.working = false, false
		if s.waiter != nil {
			s.waiter.Release()
			s.waiter = nil
		}
		s.setOpen(ctx, false)
	case OpBind:
		ok.Previous = s.bind(ctx, *req.Agent)
		if !s.open {
			s.setOpen(ctx, true)
		}
	case OpAgents:
		ok.Agents = append(ok.Agents, s.agentRefs()...)
	}
	return ok
}

// noteProcess records the harness process a request came from. A register is a harness
// starting, so it replaces the process; anything else only fills it in when it is
// unknown, so a command run from elsewhere can't keep a dead session open.
func (s *session) noteProcess(ctx context.Context, req Request) {
	if req.Process == nil || (s.proc != nil && (req.Op != OpRegister || *s.proc == *req.Process)) {
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

// reportPresence reports the presence of each agent the session holds when it changed,
// or, with renew, again while it isn't no_session, so the server doesn't let it run
// out. no_session isn't renewed: an unrenewed presence becomes it anyway.
func (s *session) reportPresence(renew bool) {
	p := s.presence()
	for ref, a := range s.agents {
		if a.adopting {
			continue
		}
		if last, ok := s.reported[ref]; ok && last == p && (!renew || p == PresenceNoSession) {
			continue
		}
		s.reported[ref] = p
		s.d.server(ref.Server).mail.put(srvMsg{presence: &ref, state: p})
	}
}

// leavePresence reports that an agent leaving the session has no session now, unless
// that is already what was reported. The session that takes the agent next reports
// after this, through the same server connection, so its report wins.
func (s *session) leavePresence(ref AgentRef) {
	if last, ok := s.reported[ref]; ok && last != PresenceNoSession {
		s.d.server(ref.Server).mail.put(srvMsg{presence: &ref, state: PresenceNoSession})
	}
	delete(s.reported, ref)
}

func (s *session) agentRefs() []AgentRef {
	refs := slices.Collect(maps.Keys(s.agents))
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
	s.open = open
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

func (s *session) saveSession(ctx context.Context) {
	rec := SessionRecord{Key: s.key, Boot: s.boot, Open: s.open, Process: s.proc, UpdatedAt: s.now()}
	if err := s.d.cfg.Journal.SaveSession(ctx, rec); err != nil {
		s.d.log.Error("save session", "session", s.key.String(), "error", err)
	}
}

// confirm marks every bundle handed to this session as received.
func (s *session) confirm(ctx context.Context) {
	for _, a := range s.agents {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded && dl.Session == s.key {
				s.setState(ctx, dl, StateConfirmed)
			}
		}
		s.maybeAck(a)
	}
}

func (s *session) setState(ctx context.Context, dl *Delivery, st State) {
	dl.State = st
	dl.UpdatedAt = s.now()
	if err := s.d.cfg.Journal.UpdateDelivery(ctx, *dl); err != nil {
		s.d.log.Error("update delivery", "delivery", dl.ID, "state", st, "error", err)
	}
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
	if s.waiter != nil && s.waiter != w {
		s.waiter.Release()
	}
	s.waiter = w
	s.working = false
	w.accepted()
	if !s.open {
		s.setOpen(ctx, true)
	}
	s.refreshAll(true)
}

// bind makes the session the one the agent's messages go to. A session fills one seat
// at a time, so an agent it held before is let go; bind returns that agent.
func (s *session) bind(ctx context.Context, agent AgentRef) *AgentRef {
	var previous *AgentRef
	for _, ref := range s.agentRefs() {
		if ref != agent {
			s.unbind(ctx, ref)
			previous = &ref
		}
	}
	if a, ok := s.agents[agent]; ok && !a.adopting {
		return previous
	}
	if err := s.d.cfg.Journal.Bind(ctx, Binding{Agent: agent, Session: s.key, BoundAt: s.now()}); err != nil {
		s.d.log.Error("bind agent", "agent", agent.Name, "board", agent.Board, "error", err)
	}
	s.agents[agent] = &agentState{ref: agent, adopting: true, deliveries: map[int64]*Delivery{}}
	if prev := s.d.setOwner(agent, s); prev != nil {
		prev.mail.put(sessionMsg{release: &agent, adopter: s})
		return previous
	}
	s.onAdopt(ctx, agent)
	return previous
}

// unbind lets an agent go with no session to take it. Bundles handed here and never
// confirmed go back to pending, and the agent's unread messages stay unread on its
// server, so whichever session binds the agent next gets both. The journal's binding
// is replaced when the session's new agent is bound.
func (s *session) unbind(ctx context.Context, agent AgentRef) {
	if a := s.agents[agent]; !a.adopting {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded || dl.State == StateHeld {
				s.setState(ctx, dl, StatePending)
			}
		}
	}
	// An agent still being adopted is given up by its previous session, which puts its
	// bundles back to pending; the adoption, when it arrives, finds no agent here.
	delete(s.agents, agent)
	s.leavePresence(agent)
	s.d.dropOwner(agent, s)
}

// onRelease gives an agent up to the session that bound it. Bundles this session never
// confirmed go to the new one.
func (s *session) onRelease(ctx context.Context, agent AgentRef, to *session) {
	a, ok := s.agents[agent]
	if ok && a.adopting {
		s.forward[agent] = to
		return
	}
	if ok {
		for _, dl := range a.deliveries {
			if dl.State == StateHanded || dl.State == StateHeld {
				s.setState(ctx, dl, StatePending)
			}
		}
		delete(s.agents, agent)
		s.leavePresence(agent)
	}
	to.mail.put(sessionMsg{adopt: &agent})
}

// onAdopt takes over an agent once no other session holds it, loading its deliveries.
func (s *session) onAdopt(ctx context.Context, agent AgentRef) {
	if to, fwd := s.forward[agent]; fwd {
		delete(s.forward, agent)
		delete(s.agents, agent)
		to.mail.put(sessionMsg{adopt: &agent})
		return
	}
	a, ok := s.agents[agent]
	if !ok || !a.adopting {
		return
	}
	ds, err := s.d.cfg.Journal.Deliveries(ctx, openStates...)
	if err != nil {
		s.d.log.Error("load deliveries", "agent", agent.Name, "error", err)
	}
	for i := range ds {
		if ds[i].Agent != agent {
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
	s.nextRefresh++
	id := s.nextRefresh
	if gate {
		s.refreshing[id] = true
	}
	s.d.server(a.ref.Server).mail.put(srvMsg{refresh: &a.ref, id: id, replyTo: s})
}

func (s *session) onInbox(ctx context.Context, r inboxResult) {
	delete(s.refreshing, r.refresh)
	a, ok := s.agents[r.agent]
	if !ok {
		return
	}
	if r.err != nil {
		if errors.Is(r.err, ErrUnauthorized) {
			s.setProblem(a, ReasonUnauthorized)
		}
		s.d.log.Warn("read inbox", "agent", a.ref.Name, "board", a.ref.Board, "error", r.err)
		return
	}
	s.setProblem(a, "")
	a.fetched = true
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
	if !s.adapter.WaitsForIdle() && s.gatherUntil.IsZero() && len(s.offers(s.queueFilter())) > 0 {
		s.gatherUntil = s.now().Add(QueueGather)
	}
	s.maybeAck(a)
}

func (s *session) setProblem(a *agentState, reason string) {
	if a.problem != reason {
		a.problem = reason
		s.d.setProblem(a.ref, reason)
	}
}

// movedTo records that the agent's read position is at cursor: deliveries entirely
// behind it are done, whoever acknowledged them.
func (s *session) movedTo(ctx context.Context, a *agentState, cursor int) {
	a.ackedUpTo = cursor
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
	urgentOnly
	notUrgent
)

func (f filter) allows(m Message) bool {
	switch f {
	case urgentOnly:
		return m.Urgent
	case notUrgent:
		return !m.Urgent
	case allMessages:
	}
	return true
}

// queueFilter is what may go into a queueing harness's queue now: during a turn, urgent
// messages wait for a tool hook, since the queue would hold them until the turn ends.
func (s *session) queueFilter() filter {
	if s.inTurn {
		return notUrgent
	}
	return allMessages
}

// newMessages returns unread messages that are in no delivery yet.
func (s *session) newMessages(a *agentState, f filter) []Message {
	t := taken(a)
	var out []Message
	for _, m := range a.unread {
		if _, in := t[m.Seq]; in || !f.allows(m) {
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
		s.d.server(a.ref.Server).mail.put(srvMsg{ack: &a.ref, upTo: point, replyTo: s})
	}
}

func (s *session) onAck(ctx context.Context, r ackResult) {
	a, ok := s.agents[r.agent]
	if !ok {
		return
	}
	a.acking = false
	if r.err != nil {
		if errors.Is(r.err, ErrUnauthorized) {
			s.setProblem(a, ReasonUnauthorized)
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
// order.
func (s *session) offers(f filter) []offer {
	now := s.now()
	var out []offer
	for _, ref := range s.agentRefs() {
		a := s.agents[ref]
		mode := s.d.mode(ref)
		if a.adopting || !a.fetched || a.problem != "" || mode == ModeOff {
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
		if again != nil {
			if f == urgentOnly {
				continue
			}
			msgs := s.messagesFor(a, again.Seqs)
			if len(msgs) == 0 {
				continue
			}
			orderForBundle(msgs)
			out = append(out, offer{agent: ref, redeliver: again.ID, msgs: msgs})
			continue
		}
		msgs := s.newMessages(a, f)
		if mode == ModeHumans {
			msgs = forHumansMode(msgs, f)
		}
		if len(msgs) > 0 {
			orderForBundle(msgs)
			out = append(out, offer{agent: ref, msgs: msgs})
		}
	}
	return out
}

// forHumansMode narrows new messages for an agent that wakes only for people: nothing
// unless a person sent one of them. A bundle at idle then carries them all, peer ones too;
// urgent messages mid-turn are only the people's.
func forHumansMode(msgs []Message, f filter) []Message {
	fromHuman := func(m Message) bool { return m.FromHuman }
	if f == urgentOnly {
		return slices.DeleteFunc(msgs, func(m Message) bool { return !fromHuman(m) })
	}
	if !slices.ContainsFunc(msgs, fromHuman) {
		return nil
	}
	return msgs
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
	if !s.open || len(s.refreshing) > 0 {
		return
	}
	if s.adapter.WaitsForIdle() {
		if s.waiter == nil {
			return
		}
	} else if s.gatherUntil.IsZero() || s.now().Before(s.gatherUntil) {
		return
	}
	s.gatherUntil = time.Time{}
	c := compose(s.offers(s.queueFilter()), BundleLimit)
	s.skip(ctx, c.tooLarge)
	if len(c.parts) == 0 {
		s.scheduleRetry()
		return
	}
	handed := s.record(ctx, c.parts, StateHanded)
	confirmed, err := s.hand(ctx, Handover{SessionID: s.key.ID, Bundle: c.text, Waiter: s.waiterOrNil()})
	if s.adapter.WaitsForIdle() {
		s.waiter = nil // a waiting hook takes one bundle, or has gone
	}
	if err == nil && s.adapter.WaitsForIdle() {
		s.working = true // the waiting hook took the bundle and wakes the session with it
	}
	switch {
	case err == nil && confirmed:
		for _, dl := range handed {
			s.setState(ctx, dl, StateConfirmed)
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
	if !s.adapter.WaitsForIdle() && len(s.offers(s.queueFilter())) > 0 {
		s.gatherUntil = s.now()
	}
	s.scheduleRetry()
	s.d.log.Info("bundle handed", "session", s.key.String(), "deliveries", len(handed), "bytes", len(c.text), "error", errText(err))
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
	return s.adapter.Hand(hctx, h)
}

func (s *session) waiterOrNil() Waiter {
	if s.waiter == nil {
		return nil
	}
	return s.waiter
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
		a := s.agents[p.agent]
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
		s.maybeAck(s.agents[p.agent])
	}
}

// handUrgent hands urgent messages to a busy session's tool hook and returns the bundle.
func (s *session) handUrgent(ctx context.Context) string {
	if !s.open {
		return ""
	}
	c := compose(s.offers(urgentOnly), BundleLimit)
	s.skip(ctx, c.tooLarge)
	if len(c.parts) == 0 {
		return ""
	}
	st := StateHanded
	if !s.adapter.WaitsForIdle() {
		// A harness with no stop hook sends no later event to confirm with; the hook's
		// output reaching it is the confirmation.
		st = StateConfirmed
	}
	s.record(ctx, c.parts, st)
	for _, p := range c.parts {
		s.maybeAck(s.agents[p.agent])
	}
	return c.text
}
