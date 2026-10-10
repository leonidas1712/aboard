package cli

import (
	"bufio"
	"context"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

type shownRead struct {
	app     *app
	request delivery.Request
	boardID string
	hold    *inboxRead
}

// beginShownRead holds existing local delivery only for this session's own seat.
func (a *app) beginShownRead(ctx context.Context, cred agentCredential, b *api.Board) *shownRead {
	key, ok := a.sessionKey()
	if !ok || cred.MemberID == "" {
		return nil
	}
	if _, sub := a.registry().Subagent(a.henv()); sub {
		return nil
	}
	p, err := a.paths()
	if err != nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	conn, err := control.Dial(callCtx, p.socket())
	if err != nil {
		return nil
	}
	_ = conn.SetDeadline(time.Now().Add(daemonCallTimeout))
	req := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID, Boot: a.env.Getenv("ABOARD_BOOT")}
	var resp delivery.Response
	err = delivery.WriteFrame(conn, req)
	if err == nil {
		err = delivery.ReadFrame(bufio.NewReader(conn), &resp)
	}
	_ = conn.Close()
	if err != nil || resp.Error != nil || resp.Boot == "" || (req.Boot != "" && req.Boot != resp.Boot) {
		return nil
	}
	for _, ref := range resp.Agents {
		if ref.Server != cred.Server || ref.MemberID != cred.MemberID {
			continue
		}
		hold := a.startInboxRead(ctx, ref, false)
		if hold.conn == nil || hold.boot != resp.Boot || hold.generation == 0 {
			hold.done()
			return nil
		}
		req.Op, req.Boot, req.Agent = delivery.OpShown, hold.boot, &ref
		req.Generation = hold.generation
		return &shownRead{app: a, request: req, boardID: b.Id, hold: hold}
	}
	return nil
}

func (r *shownRead) done() {
	if r != nil {
		r.hold.done()
	}
}

// report is best-effort: a missing or old daemon cannot make the successful read fail.
func (r *shownRead) report(ctx context.Context, msgs []api.Message) {
	if r == nil || len(msgs) == 0 {
		return
	}
	req := r.request
	for _, m := range msgs {
		req.ShownMessages = append(req.ShownMessages, delivery.ShownMessage{BoardID: r.boardID, MemberID: req.Agent.MemberID, MessageID: m.Id, Seq: m.Seq})
	}
	p, err := r.app.paths()
	if err != nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	conn, err := control.Dial(callCtx, p.socket())
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(daemonCallTimeout))
	if delivery.WriteFrame(conn, req) != nil {
		return
	}
	var resp delivery.Response
	_ = delivery.ReadFrame(bufio.NewReader(conn), &resp)
}

// inboxShown keeps the existing hold through stdout; old servers without board ids
// keep their read behavior but cannot supply exact-message pairing evidence.
func (a *app) inboxShown(ref delivery.AgentRef, in *api.Inbox, hold *inboxRead) *shownRead {
	key, ok := a.sessionKey()
	if !ok || in.BoardId == nil || *in.BoardId == "" || hold.boot == "" || hold.generation == 0 || hold.conn == nil {
		return nil
	}
	if _, sub := a.registry().Subagent(a.henv()); sub {
		return nil
	}
	if boot := a.env.Getenv("ABOARD_BOOT"); boot != "" && boot != hold.boot {
		return nil
	}
	if in.MemberId == nil || *in.MemberId != ref.MemberID {
		return nil
	}
	return &shownRead{app: a, boardID: *in.BoardId, hold: hold, request: delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpShown, Harness: key.Harness, Session: key.ID, Boot: hold.boot, Agent: &ref, Generation: hold.generation}}
}
