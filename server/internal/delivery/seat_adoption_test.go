package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestRenamedSeatAdoptsConfirmedDeliveryWithoutResending(t *testing.T) {
	r := newRig(t)
	old := reviewer
	old.MemberID = "mem_reviewer"
	renamed := old
	renamed.Name = "reader"
	seq := r.post(renamed, "already received before the rename", false)
	id, err := r.journal.AddDelivery(context.Background(), delivery.Delivery{
		Agent: old, Session: delivery.SessionKey{Harness: "codex", ID: "old-session"}, Boot: "old-boot",
		State: delivery.StateConfirmed, Seqs: []int{seq},
		CreatedAt: r.clock.Now(), UpdatedAt: r.clock.Now(), AcceptedAt: r.clock.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	r.bind("codex", "new-session", renamed)
	r.eventually("the adopted delivery's acknowledgement", 500*time.Millisecond, func() bool {
		ds, err := r.journal.Deliveries(context.Background(), delivery.StateDone)
		return r.server.Cursor(renamed) == seq && err == nil && len(ds) != 0
	})
	if handed := r.codex.Handed("new-session"); len(handed) != 0 {
		t.Fatalf("confirmed old-name delivery was sent again: %+v", handed)
	}
	ds, err := r.journal.Deliveries(context.Background(), delivery.StateDone)
	if err != nil || len(ds) != 1 || ds[0].ID != id || ds[0].Agent.MemberID != old.MemberID || ds[0].AcceptedAt.IsZero() {
		t.Fatalf("adoption lost the confirmed delivery: %+v, %v", ds, err)
	}
}
