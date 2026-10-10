package delivery_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

type queuedIssuerServer struct {
	*deliverytest.FakeServer
	refuse  atomic.Bool
	refused atomic.Int32
}

func (s *queuedIssuerServer) Inbox(ctx context.Context, agent delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	if s.refuse.Load() {
		s.refused.Add(1)
		return nil, 0, nil, delivery.ErrUnauthorized
	}
	return s.FakeServer.Inbox(ctx, agent)
}

func TestQueuedObservationFiltersIssuerBeforeFreshAccess(t *testing.T) {
	r := newRig(t)
	r.status()
	r.stop()
	first := &queuedIssuerServer{FakeServer: r.server}
	other := deliverytest.NewFakeServer()
	const otherURL = "https://other.example"
	r.configure = func(c *delivery.Config) {
		c.Connect = func(issuer string) delivery.Server {
			if issuer == serverURL {
				return first
			}
			if issuer == otherURL {
				return other
			}
			t.Errorf("unexpected issuer %s", issuer)
			return other
		}
	}
	r.start()
	req := delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "issuer-queue", Boot: "b1"}
	r.ok(req)
	a, b := reviewer, reviewer
	b.Server = otherURL
	r.bind("codex", req.Session, a)
	r.bind("codex", req.Session, b)
	req.Op = delivery.OpPrompt
	r.ok(req)
	r.server.Post(a, delivery.Message{ID: "msg_same", BoardID: "brd_same", Body: "first pending", FromName: "writer"})
	other.Post(b, delivery.Message{ID: "msg_same", BoardID: "brd_same", Body: "second pending", FromName: "writer"})
	req.Op = delivery.OpQueued
	initial := r.ok(req)
	if initial.Queued == nil || initial.Queued.Count != 2 {
		t.Fatalf("equal issuer-local identities merged: %+v", initial)
	}
	first.refuse.Store(true)
	req.Server = otherURL
	got := r.ok(req)
	if first.refused.Load() != 0 {
		t.Fatal("selected queue read foreign issuer")
	}
	if got.Queued == nil || got.Queued.Count != 1 || len(got.Agents) != 1 || got.Agents[0].Server != otherURL {
		t.Fatalf("selected queue: %+v", got)
	}
	m := got.Queued.Messages[0]
	if m.Server != otherURL || m.BoardID != "brd_same" || m.MemberID != b.MemberID || m.MessageID != "msg_same" {
		t.Fatalf("issuer-bound identity: %+v", m)
	}
	if r.server.Cursor(a) != 0 || other.Cursor(b) != 0 || len(r.codex.Handed(req.Session)) != 0 {
		t.Fatal("queue observation acknowledged or delivered messages")
	}
}
