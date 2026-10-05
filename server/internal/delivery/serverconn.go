package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// serverRequestTimeout bounds one inbox read or acknowledgement.
const serverRequestTimeout = 30 * time.Second

// srvMsg is one request to a server connection's goroutine.
type srvMsg struct {
	// watch adds an agent whose inbox is read when its board's head moves.
	watch *AgentRef
	// refresh reads an agent's inbox now and answers replyTo with id.
	refresh *AgentRef
	id      int64
	replyTo *session
	// ack acknowledges an agent's messages up to upTo and answers replyTo.
	ack  *AgentRef
	upTo int
	// head is a head change from the stream; connected says the stream (re)opened.
	head      *Head
	connected bool
	// presence reports an agent's presence.
	presence *AgentRef
	state    Presence
	mode     Mode
}

// serverConn follows one server. Its watched agents are changed only by run; the
// follow goroutine only reports heads into the mailbox.
type serverConn struct {
	d    *Daemon
	url  string
	srv  Server
	mail *mailbox[srvMsg]

	watched map[AgentRef]bool
	// gone holds watched agents whose board answered board_not_found, whose inboxes a
	// head change no longer reads. Watching an agent again, when a session binds it,
	// clears it.
	gone map[AgentRef]bool

	mu        sync.Mutex
	connected bool
	problem   string
}

func (c *serverConn) snapshot() (connected bool, problem string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected, c.problem
}

func (c *serverConn) setStatus(connected bool, problem string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected, c.problem = connected, problem
}

func (c *serverConn) run(ctx context.Context) error {
	c.d.g.Go(func() error { return c.follow(ctx) })
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.mail.ready():
			c.handle(ctx, c.mail.take())
		}
	}
}

func (c *serverConn) handle(ctx context.Context, batch []srvMsg) {
	boards := map[string]bool{}
	all := false
	for _, m := range batch {
		switch {
		case m.watch != nil:
			c.watched[*m.watch] = true
			delete(c.gone, *m.watch)
		case m.head != nil && m.head.Read != nil:
			// The server says an agent read up to a point, whoever acknowledged: its session
			// drops what is at or below it from what it would hand over or announce.
			agent := AgentRef{Server: c.url, Board: m.head.Board, Name: m.head.Read.Agent}
			if s := c.d.owner(agent); s != nil {
				s.mail.put(sessionMsg{read: &readMove{agent: agent, upTo: m.head.Read.UpTo}})
			}
		case m.head != nil:
			boards[m.head.Board] = true
		case m.connected:
			all = true
		}
	}
	for agent := range c.watched {
		if (all || boards[agent.Board]) && !c.gone[agent] {
			if s := c.d.owner(agent); s != nil {
				s.mail.put(sessionMsg{inbox: c.fetch(ctx, agent, 0)})
			}
		}
	}
	for _, m := range batch {
		switch {
		case m.refresh != nil:
			m.replyTo.mail.put(sessionMsg{inbox: c.fetch(ctx, *m.refresh, m.id)})
		case m.ack != nil:
			err := c.ack(ctx, *m.ack, m.upTo)
			m.replyTo.mail.put(sessionMsg{ack: &ackResult{agent: *m.ack, upTo: m.upTo, err: err}})
		}
	}
	c.reportPresence(ctx, batch)
}

// errStillGone is what a request queued for an agent whose board is gone gets in place
// of a request to the server, which would answer the same.
var errStillGone = fmt.Errorf("%w: not asked again", ErrBoardGone)

// ack acknowledges an agent's messages, unless its board is gone.
func (c *serverConn) ack(ctx context.Context, agent AgentRef, upTo int) error {
	if c.gone[agent] {
		return errStillGone
	}
	actx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
	defer cancel()
	err := c.srv.Ack(actx, agent, upTo)
	if errors.Is(err, ErrBoardGone) {
		c.gone[agent] = true
	}
	return err
}

// reportPresence sends the latest presence in batch for each agent whose board isn't
// gone. A report that fails for a reason that may pass is only logged: the session
// reports again within PresenceRenew, and until then the server keeps the last presence
// it had. A refusal that stops the agent (its board is gone, or its token is rejected)
// goes to the session that holds the agent, as a failed inbox read would.
func (c *serverConn) reportPresence(ctx context.Context, batch []srvMsg) {
	latest := map[AgentRef]srvMsg{}
	var order []AgentRef
	for _, m := range batch {
		if m.presence == nil {
			continue
		}
		if _, seen := latest[*m.presence]; !seen {
			order = append(order, *m.presence)
		}
		latest[*m.presence] = m
	}
	for _, agent := range order {
		if c.gone[agent] {
			continue
		}
		m := latest[agent]
		pctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
		err := c.srv.SetPresence(pctx, agent, m.state, m.mode)
		cancel()
		if err == nil {
			continue
		}
		c.d.log.Warn("report presence", "agent", agent.Name, "board", agent.Board, "presence", m.state, "error", err)
		if errors.Is(err, ErrBoardGone) {
			c.gone[agent] = true
		}
		if problemOf(err) != "" {
			if s := c.d.owner(agent); s != nil {
				s.mail.put(sessionMsg{refused: &refusal{agent: agent, err: err}})
			}
		}
	}
}

func (c *serverConn) fetch(ctx context.Context, agent AgentRef, refresh int64) *inboxResult {
	if c.gone[agent] {
		return &inboxResult{agent: agent, err: errStillGone, refresh: refresh}
	}
	fctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
	defer cancel()
	msgs, cursor, err := c.srv.Inbox(fctx, agent)
	if errors.Is(err, ErrBoardGone) {
		c.gone[agent] = true
	}
	return &inboxResult{agent: agent, msgs: msgs, cursor: cursor, err: err, refresh: refresh}
}

// followOnce follows the stream until it fails, recording why. It reports whether the
// daemon is stopping.
func (c *serverConn) followOnce(ctx context.Context, connected func()) (stopping bool) {
	err := c.srv.Follow(ctx, func() {
		connected()
		c.setStatus(true, "")
		c.mail.put(srvMsg{connected: true})
	}, func(h Head) {
		c.mail.put(srvMsg{head: &h})
	})
	if ctx.Err() != nil {
		return true
	}
	problem := "server_unreachable"
	if errors.Is(err, ErrLoginMissing) {
		problem = "login_missing"
	}
	c.setStatus(false, problem)
	c.d.log.Warn("server stream failed", "server", c.url, "error", err)
	return false
}

// follow keeps the server's head stream open, reconnecting with backoff from 1 second
// up to 60. Every (re)connection reads every watched inbox once, so a head change
// missed while disconnected costs nothing.
func (c *serverConn) follow(ctx context.Context) error {
	failures := 0
	for ctx.Err() == nil {
		if c.followOnce(ctx, func() { failures = 0 }) {
			break
		}
		failures++
		c.d.log.Warn("server stream closed", "server", c.url, "retry_in", backoff(failures))
		select {
		case <-ctx.Done():
		case <-c.d.cfg.Clock.After(backoff(failures)):
		}
	}
	return nil
}
