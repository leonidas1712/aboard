package delivery

import (
	"context"
	"errors"
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
		if all || boards[agent.Board] {
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
			actx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
			err := c.srv.Ack(actx, *m.ack, m.upTo)
			cancel()
			m.replyTo.mail.put(sessionMsg{ack: &ackResult{agent: *m.ack, upTo: m.upTo, err: err}})
		}
	}
	c.reportPresence(ctx, batch)
}

// reportPresence sends the latest presence in batch for each agent. A report that
// fails is only logged: the session reports again within PresenceRenew, and until then
// the server keeps the last presence it had.
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
		m := latest[agent]
		pctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
		err := c.srv.SetPresence(pctx, agent, m.state, m.mode)
		cancel()
		if err != nil {
			c.d.log.Warn("report presence", "agent", agent.Name, "board", agent.Board, "presence", m.state, "error", err)
		}
	}
}

func (c *serverConn) fetch(ctx context.Context, agent AgentRef, refresh int64) *inboxResult {
	fctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
	defer cancel()
	msgs, cursor, err := c.srv.Inbox(fctx, agent)
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
