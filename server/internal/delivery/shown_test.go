package delivery_test

import (
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestShownMessagesSuppressOnlyExactOwnSessionIdentitiesAcrossRestart(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "shown-ledger", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "shown-ledger", reviewer)
	r.ok(req(delivery.OpPrompt))
	first := r.server.Post(reviewer, delivery.Message{ID: "msg_shown", BoardID: "brd_docs", Body: "already read in full"})
	second := r.server.Post(reviewer, delivery.Message{ID: "msg_gap", BoardID: "brd_docs", Body: "unseen gap"})
	third := r.server.Post(reviewer, delivery.Message{ID: "msg_shown_later", BoardID: "brd_docs", Body: "also read in full"})
	shown := req(delivery.OpShown)
	shown.Agent = &reviewer
	bindings, err := r.journal.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bindings {
		if b.Agent.Key() == reviewer.Key() {
			shown.Generation = b.Generation
		}
	}
	shown.ShownMessages = []delivery.ShownMessage{{BoardID: "brd_docs", MemberID: reviewer.MemberID, MessageID: "msg_shown", Seq: first}, {BoardID: "brd_docs", MemberID: reviewer.MemberID, MessageID: "msg_shown_later", Seq: third}}
	if got := r.ok(shown); !got.Shown {
		t.Fatalf("observation not committed: %+v", got)
	}
	if r.server.Cursor(reviewer) != 0 {
		t.Fatal("shown read acknowledged messages")
	}
	r.stop()
	r.start()
	r.ok(req(delivery.OpRegister))
	r.ok(req(delivery.OpPrompt))
	if q := r.ok(req(delivery.OpQueued)).Queued; q == nil || q.Count != 1 || q.Messages[0].Seq != second {
		t.Fatalf("restart inferred a gap or forgot shown identities: %+v", q)
	}
	hand := req(delivery.OpTurnStart)
	got := r.ok(hand)
	if !strings.Contains(got.Bundle, "unseen gap") || strings.Contains(got.Bundle, "read in full") {
		t.Fatalf("fresh bundle: %s", got.Bundle)
	}
}

func TestShownReadCannotSuppressAReplacementSeatGeneration(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "shown-rotation", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "shown-rotation", reviewer)
	r.ok(req(delivery.OpPrompt))
	bindings, err := r.journal.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var generation uint64
	for _, b := range bindings {
		if b.Agent.Key() == reviewer.Key() {
			generation = b.Generation
		}
	}
	seq := r.server.Post(reviewer, delivery.Message{ID: "msg_late_read", BoardID: "brd_docs", Body: "old read output"})
	shown := req(delivery.OpShown)
	shown.Agent, shown.Generation = &reviewer, generation
	shown.ShownMessages = []delivery.ShownMessage{{BoardID: "brd_docs", MemberID: reviewer.MemberID, MessageID: "msg_late_read", Seq: seq}}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "replacement", Boot: "b2"})
	r.bind("codex", "replacement", reviewer)
	r.bind("codex", "shown-rotation", reviewer)
	if got := r.call(shown); got.Error == nil {
		t.Fatal("old read suppressed replacement generation")
	}
	observations, err := r.journal.ShownMessages(t.Context(), shown.Key(), "b1")
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatal("stale read persisted")
	}
}
