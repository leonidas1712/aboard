package delivery

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"
)

// The extension connection (spec/control.md): a harness extension that runs inside the
// harness's own process holds one connection per session for as long as the session
// runs. Its hello registers the session; while the session is idle the connection is
// the session's waiter, so bundles go over it; the extension confirms each by id; and
// the connection closing closes the session.

// serveExtension keeps a harness extension's connection until the session says goodbye,
// the connection closes, or another hello for the same session replaces it.
func (d *Daemon) serveExtension(ctx context.Context, conn net.Conn, r *bufio.Reader, hello Request) {
	key := hello.Key()
	ad, ok := d.adapters[hello.Harness]
	switch {
	case !ok:
		_ = WriteFrame(conn, errorResponse("invalid_request", fmt.Sprintf("%q is not a harness the delivery daemon knows.", hello.Harness),
			"Use "+d.harnessNames()+"."))
		return
	case hello.Session == "" || hello.Boot == "":
		_ = WriteFrame(conn, errorResponse("invalid_request", "A hello needs the session's id and a boot id.",
			"Send the harness's session id, and a random boot id made once per harness process."))
		return
	case hello.Subagent != "":
		_ = WriteFrame(conn, errorResponse("subagent_session",
			"The hello comes from subagent "+hello.Subagent+" of session "+key.String()+", and messages go only to the root conversation.",
			"Connect only from the session's own conversation; a subagent's aboard commands may read the board."))
		return
	}
	vctx, cancel := context.WithTimeout(ctx, harnessCallTimeout)
	err := ad.Validate(vctx, hello.Session)
	cancel()
	if resp, failed := validationResponse(key, err); failed {
		_ = WriteFrame(conn, resp)
		return
	}
	// An extension can't always read its process's start time, which tells a running
	// process from an earlier one with the same id, so the daemon reads it now.
	if p := hello.Process; p != nil && p.Start == 0 && d.cfg.Processes != nil {
		if start, ok := d.cfg.Processes.StartTime(p.PID); ok {
			hello.Process = &Process{PID: p.PID, Start: start}
		}
	}
	s := d.session(key, true)
	if s == nil {
		return
	}
	c := &extConn{conn: conn, handoffs: map[string]bool{}, supportsHandoffs: slices.Contains(hello.Capabilities, CapabilityHandoffV1), took: map[int64]bool{}, registered: make(chan bool, 1), signal: make(chan struct{}, 1), gone: make(chan struct{})}
	defer close(c.gone)
	s.mail.put(sessionMsg{req: hello, ext: c})
	select {
	case accepted := <-c.registered:
		if !accepted {
			return
		}
	case <-ctx.Done():
		return
	}
	if hello.Launch != "" {
		// The bind goes through the session's mailbox after the hello, so the session is
		// registered first.
		if resp := d.bindLaunch(ctx, hello, hello.Launch, Response{}); resp.Error != nil {
			d.log.Warn("launch ticket: couldn't bind the session", "session", key.String(), "error", resp.Error.Message)
		}
	}
	goodbye := false
	for !goodbye {
		var m Request
		if err := ReadFrame(r, &m); err != nil {
			break
		}
		switch m.Op {
		case OpReceived:
			if m.HandoffID != "" {
				c.receivedHandoff(m.HandoffID)
			} else {
				c.received(m.ID)
			}
		case OpPrompt, OpTurnEnd, OpGoodbye:
			// These name no session: the connection's hello did.
			req := Request{Op: m.Op, Harness: hello.Harness, Session: hello.Session, Boot: hello.Boot}
			if m.Op == OpGoodbye {
				req.Op, goodbye = OpEnd, true
			}
			s.mail.put(sessionMsg{req: req, from: c})
		default:
			_ = c.write(errorResponse("invalid_request", fmt.Sprintf("The delivery daemon has no operation %q on an extension connection.", m.Op),
				"Use the extension that this aboard installs: run aboard init."))
		}
	}
	if !goodbye && ctx.Err() == nil {
		s.mail.put(sessionMsg{extGone: c})
	}
}

// extConn is one harness extension's connection. While its session is idle it is the
// session's waiter.
type extConn struct {
	conn net.Conn
	// wmu keeps writes whole: the session's goroutine and the reader both write.
	wmu sync.Mutex
	// mu guards took, the deliveries the extension confirmed.
	mu               sync.Mutex
	took             map[int64]bool
	handoffs         map[string]bool
	pendingHandoff   string
	supportsHandoffs bool
	registered       chan bool
	signal           chan struct{}
	gone             chan struct{}
}

var _ Waiter = (*extConn)(nil)
var _ HandoffWaiter = (*extConn)(nil)

// write sends one frame, giving up after waiterWriteTimeout so an extension that stopped
// reading can't hold up its session.
func (c *extConn) write(r Response) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(waiterWriteTimeout))
	return WriteFrame(c.conn, r)
}

// received records that the extension added a delivery to its session.
func (c *extConn) received(id int64) {
	c.mu.Lock()
	c.took[id] = true
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

func (c *extConn) has(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.took[id]
}

// Deliver sends the bundle with its delivery id and waits for the extension to confirm
// that id. An extension answers received again, without adding the bundle twice, for a
// delivery sent again after it reconnected.
func (c *extConn) Deliver(ctx context.Context, id int64, bundle string) error {
	if err := c.write(Response{V: ProtocolVersion, Event: EventDeliver, ID: id, Bundle: bundle}); err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	return c.waitLegacy(ctx, id)
}

func (c *extConn) waitLegacy(ctx context.Context, id int64) error {
	for {
		if c.has(id) {
			return nil
		}
		select {
		case <-c.signal:
		case <-c.gone:
			if c.has(id) {
				return nil
			}
			return ErrBusy
		case <-ctx.Done():
			return fmt.Errorf("wait for the extension to confirm delivery %d: %w", id, ctx.Err())
		}
	}
}

// SupportsHandoffs reports only this live connection's negotiated capability.
func (c *extConn) SupportsHandoffs() bool {
	if c == nil || !c.supportsHandoffs {
		return false
	}
	select {
	case <-c.gone:
		return false
	default:
		return true
	}
}

func (c *extConn) receivedHandoff(id string) {
	c.mu.Lock()
	if c.supportsHandoffs && id == c.pendingHandoff {
		c.handoffs[id] = true
	}
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

func (c *extConn) hasHandoff(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handoffs[id]
}

func (c *extConn) DeliverHandoff(ctx context.Context, h Handover) error {
	class := ClassMixed
	if h.Class == ClassOwnerOnly {
		class = ClassOwnerOnly
	}
	if h.HandoffID == "" {
		if err := c.write(Response{V: ProtocolVersion, Event: EventDeliver, ID: h.ID, Bundle: h.Bundle, DeliveryClass: class}); err != nil {
			return fmt.Errorf("%w: %w", ErrBusy, err)
		}
		return c.waitLegacy(ctx, h.ID)
	}
	if !c.SupportsHandoffs() {
		return ErrExtensionOutdated
	}
	c.mu.Lock()
	c.pendingHandoff = h.HandoffID
	c.mu.Unlock()
	if err := c.write(Response{V: ProtocolVersion, Event: EventDeliver, HandoffID: h.HandoffID, Bundle: h.Bundle, DeliveryClass: class}); err != nil {
		return fmt.Errorf("%w: %w", ErrBusy, err)
	}
	for {
		if c.hasHandoff(h.HandoffID) {
			return nil
		}
		select {
		case <-c.signal:
		case <-c.gone:
			if c.hasHandoff(h.HandoffID) {
				return nil
			}
			return ErrBusy
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Release does nothing: an extension's connection stays open when its session starts a
// turn, and the extension knows the turn started, since it said so.
func (c *extConn) Release() {}

// replace tells the extension another connection now serves its session, so it doesn't
// connect again, and closes this one.
func (c *extConn) replace() {
	_ = c.write(Response{V: ProtocolVersion, Event: EventRelease})
	_ = c.conn.Close()
}

// onHello registers the session an extension connects for, as a session-start hook's
// register does, and makes the connection its waiter. A session that comes back with
// the same id is bound again to the agent it filled. Bundles handed to an earlier
// connection and never confirmed go again; the extension skips those it already added.
func (s *session) onHello(ctx context.Context, req Request, c *extConn) {
	accepted := false
	defer func() {
		if c.registered != nil {
			c.registered <- accepted
		}
	}()
	if len(s.agents) > 1 && !c.SupportsHandoffs() {
		_ = c.write(errorResponse("extension_outdated", "This extension cannot deliver to several board seats.", "Run aboard init, then restart the harness."))
		_ = c.conn.Close()
		return
	}
	if old := s.ext; old != nil && old != c {
		if s.waiter == old {
			s.waiter = nil
		}
		old.replace()
	}
	s.ext = c
	reopened := s.started && !s.open
	s.noteProcess(ctx, req)
	s.inTurn, s.working = false, false
	// The extension reports every turn, so a bundle that starts none can be told.
	s.seenTurns = true
	if req.Boot != s.boot {
		s.newBoot(ctx, req.Boot)
	} else {
		s.unhand(ctx)
	}
	if !s.open {
		s.setOpen(ctx, true)
	}
	welcome := Response{V: ProtocolVersion, Event: EventWelcome, Capabilities: negotiatedCapabilities(c), Boot: s.boot, Reopened: reopened, Agents: s.agentRefs()}
	if len(welcome.Agents) == 0 {
		welcome.Lost = s.lost
	}
	welcome.Mode, welcome.Note = s.startNote(reopened, false)
	if err := c.write(welcome); err != nil {
		s.d.log.Warn("extension connection: couldn't send welcome", "session", s.key.String(), "error", err)
		return
	}
	accepted = true
	s.waiter = c
	s.refreshAll(true)
	pid := 0
	if req.Process != nil {
		pid = req.Process.PID
	}
	s.d.log.Info("session started", "session", s.key.String(), "source", req.Source, "reopened", reopened,
		"agents", len(welcome.Agents), "lost", welcome.Lost != nil, "connection", "extension", "resumed", req.Resumed,
		"pid", pid, "cwd", req.Cwd, "harness_version", req.HarnessVersion, "extension_version", req.ExtensionVersion)
}

// onExtension handles what an extension reports on its connection: a turn started, a
// turn ended, or the session closed. A connection that another hello replaced has no
// say any more.
func (s *session) onExtension(ctx context.Context, req Request, c *extConn) {
	if c != s.ext {
		return
	}
	s.onRequest(ctx, req)
	switch req.Op {
	case OpTurnEnd:
		// Idle again: the connection waits for the next bundle, which holds everything
		// sent before now.
		s.waiter = c
		s.refreshAll(true)
	case OpEnd:
		s.ext = nil
		s.d.log.Info("session closed", "session", s.key.String(), "connection", "extension")
	}
}

// onExtensionGone closes the session of an extension whose connection closed without a
// goodbye: the harness process ended, or lost its connection and connects again.
func (s *session) onExtensionGone(ctx context.Context, c *extConn) {
	if c != s.ext {
		return
	}
	s.ext = nil
	if s.waiter == c {
		s.waiter = nil
	}
	s.inTurn, s.working = false, false
	if s.open {
		s.d.log.Info("closing session: its extension connection closed", "session", s.key.String())
		s.setOpen(ctx, false)
	}
}

func negotiatedCapabilities(c *extConn) []string {
	if c.SupportsHandoffs() {
		return []string{CapabilityHandoffV1}
	}
	return nil
}
