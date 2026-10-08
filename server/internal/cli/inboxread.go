package cli

import (
	"bufio"
	"context"
	"net"
	"slices"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

// inboxRead is a command reading an agent's inbox while the delivery daemon on this
// machine holds the agent's deliveries, so a message the command shows can't also be
// handed to the agent's session, and the session never gets or is told about it again.
// How far the command then acknowledges reaches the daemon from the server. With no
// daemon, or none holding the agent, it does nothing.
type inboxRead struct {
	boot       string
	generation uint64
	conn       net.Conn
	r          *bufio.Reader
	// received are messages a session here received that the server still counts
	// unread, because the daemon's acknowledgement hasn't landed yet.
	received []int
}

// startInboxRead asks a running delivery daemon to hold the agent's deliveries while
// the command reads its inbox. It never starts a daemon: one that isn't running has
// nothing to hold. With confirm, a command the agent runs in its own session (not in a
// subagent's) also tells the daemon the session is running a turn, which confirms a
// bundle handed before the command started. Peeking confirms nothing: it changes no
// state.
func (a *app) startInboxRead(ctx context.Context, agent delivery.AgentRef, confirm bool) *inboxRead {
	p, err := a.paths()
	if err != nil {
		return &inboxRead{}
	}
	dctx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	conn, err := control.Dial(dctx, p.socket())
	if err != nil {
		return &inboxRead{}
	}
	req := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpInbox, Agent: &agent}
	if key, ok := a.sessionKey(); ok && confirm {
		if _, sub := a.registry().Subagent(a.henv()); !sub {
			req.Harness, req.Session, req.Boot, req.Started = key.Harness, key.ID, a.env.Getenv("ABOARD_BOOT"), a.started
		}
	}
	rd := &inboxRead{conn: conn, r: bufio.NewReader(conn)}
	var resp delivery.Response
	_ = conn.SetDeadline(time.Now().Add(daemonCallTimeout))
	if delivery.WriteFrame(conn, req) != nil || delivery.ReadFrame(rd.r, &resp) != nil || resp.Error != nil || !resp.Held {
		_ = conn.Close()
		return &inboxRead{}
	}
	rd.received = resp.Received
	rd.boot, rd.generation = resp.Boot, resp.Generation
	return rd
}

// unread returns the messages the agent hasn't received, leaving out those a session
// here already has.
func (rd *inboxRead) unread(msgs []api.Message) []api.Message {
	return slices.DeleteFunc(slices.Clone(msgs), func(m api.Message) bool { return slices.Contains(rd.received, m.Seq) })
}

// done ends the hold.
func (rd *inboxRead) done() {
	if rd.conn != nil {
		_ = rd.conn.Close()
	}
}
