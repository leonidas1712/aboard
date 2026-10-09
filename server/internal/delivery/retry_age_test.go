package delivery_test

import (
	"io"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestRetryAfterRestartKeepsItsOriginalAgeAndManifest(t *testing.T) {
	r := newRig(t)
	r.codex.FailWith(io.ErrUnexpectedEOF)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "retry-age", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "retry-age", reviewer)
	r.server.Post(reviewer, delivery.Message{ID: "msg_old", BoardID: "brd_docs", At: r.clock.Now().Add(-2 * time.Hour), FromName: "peer", Sender: "owner_agent", To: []string{"@" + reviewer.Name}, Body: "old message"})
	r.eventually("failed handoff allocated", delivery.QueueGather+time.Second, func() bool { return r.codex.Attempts() > 0 })
	before, err := r.journal.Handoffs(t.Context())
	if err != nil || len(before) != 1 {
		t.Fatalf("initial manifest: %+v %v", before, err)
	}
	r.stop()
	r.clock.Advance(2 * time.Hour)
	r.codex.FailWith(nil)
	r.start()
	r.ok(req(delivery.OpRegister))
	r.eventually("retry handed", delivery.QueueGather+time.Second, func() bool { return len(r.codex.Handed("retry-age")) > 0 })
	after, err := r.journal.Handoffs(t.Context())
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].PayloadHash != before[0].PayloadHash {
		t.Fatalf("retry changed age payload/manifest: before=%+v after=%+v err=%v", before, after, err)
	}
}
