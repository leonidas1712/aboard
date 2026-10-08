package delivery_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestQueuedObservationIsFreshReadOnlyAndBootBound(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "queue-observation", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "queue-observation", reviewer)
	r.ok(req(delivery.OpPrompt))
	seq := r.server.Post(reviewer, delivery.Message{ID: "msg_pending", BoardID: "brd_docs", FromName: "writer", Body: "still pending"})
	got := r.ok(req(delivery.OpQueued))
	if got.Queued == nil || got.Queued.Count != 1 || len(got.Queued.Messages) != 1 {
		t.Fatalf("queue observation: %+v", got)
	}
	m := got.Queued.Messages[0]
	if m.BoardID != "brd_docs" || m.MemberID != reviewer.MemberID || m.MessageID != "msg_pending" || m.Seq != seq || m.Boundary != "turn_end" {
		t.Fatalf("queue identity: %+v", m)
	}
	if r.server.Cursor(reviewer) != 0 || len(r.codex.Handed("queue-observation")) != 0 {
		t.Fatal("observation acknowledged or handed text")
	}
	if err := r.server.Ack(context.Background(), reviewer, seq); err != nil {
		t.Fatal(err)
	}
	if q := r.ok(req(delivery.OpQueued)).Queued; q == nil || q.Count != 0 {
		t.Fatalf("already-read message remains queued: %+v", q)
	}
	stale := req(delivery.OpQueued)
	stale.Boot = "earlier-boot"
	if r.call(stale).Error == nil {
		t.Fatal("stale boot observed the current session")
	}
	r.ok(req(delivery.OpEnd))
	if r.call(req(delivery.OpQueued)).Error == nil {
		t.Fatal("ended session reported empty queue instead of unknown")
	}
}

func TestQueuedObservationRetainsAcceptedAdmissionUntilTurnStarts(t *testing.T) {
	r := newRig(t)
	r.server.HoldModes()
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "accepted-queue", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "accepted-queue", reviewer)
	r.ok(req(delivery.OpPrompt))
	r.ok(req(delivery.OpTurnEnd))
	seq := r.server.Post(reviewer, delivery.Message{ID: "msg_accepted", BoardID: "brd_docs", Body: "accepted but not started"})
	r.eventually("queue admission acknowledged", delivery.QueueGather, func() bool { return r.server.Cursor(reviewer) == seq })
	got := r.ok(req(delivery.OpQueued))
	if got.Queued == nil || got.Queued.Count != 1 || got.Queued.Messages[0].MessageID != "msg_accepted" {
		t.Fatalf("accepted admission disappeared behind cursor: %+v", got)
	}
	r.stop()
	r.start()
	if q := r.ok(req(delivery.OpQueued)).Queued; q == nil || q.Count != 1 {
		t.Fatalf("restart lost queue admission: %+v", q)
	}
	r.ok(req(delivery.OpPrompt))
	if q := r.ok(req(delivery.OpQueued)).Queued; q == nil || q.Count != 0 {
		t.Fatalf("turn start left admission queued: %+v", q)
	}
}
