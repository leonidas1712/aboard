package delivery_test

import (
	"context"
	"errors"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestVerifiedLegacyIdentityIsResolvedBeforeRestore(t *testing.T) {
	r := newRig(t)
	r.register("old-session", "old-boot")
	r.bind("claude-code", "old-session", reviewer)
	if err := r.journal.SetMode(context.Background(), reviewer, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	r.stop()
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		ref.MemberID = "mem_original"
		return ref, nil
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "old-session"})
	if len(got.Agents) != 1 || got.Agents[0].MemberID != "mem_original" {
		t.Fatalf("restored identity: %+v", got)
	}
	mode := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &got.Agents[0]})
	if mode.Mode != delivery.ModeHumans {
		t.Fatalf("restored mode: %+v", mode)
	}
}

func TestUnresolvedLegacyStateIsNotGivenToASameNameReplacement(t *testing.T) {
	r := newRig(t)
	r.register("old-session", "old-boot")
	r.bind("claude-code", "old-session", reviewer)
	if err := r.journal.SetMode(context.Background(), reviewer, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.AddDelivery(context.Background(), delivery.Delivery{
		Agent:   reviewer,
		Session: delivery.SessionKey{Harness: "claude-code", ID: "old-session"}, State: delivery.StateConfirmed,
		Seqs: []int{7}, CreatedAt: r.clock.Now(), UpdatedAt: r.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	r.stop()
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		if ref.MemberID == "" {
			return delivery.AgentRef{}, delivery.ErrUnauthorized
		}
		return ref, nil
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "old-session"})
	if len(got.Agents) != 0 {
		t.Fatalf("unverified seat was restored: %+v", got)
	}
	fresh := reviewer
	fresh.MemberID = "mem_replacement"
	r.bind("claude-code", "old-session", fresh)
	mode := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &fresh})
	if mode.Mode != delivery.ModeFocused {
		t.Fatalf("replacement inherited old mode: %+v", mode)
	}
	deliveries, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
	if err != nil || len(deliveries) != 1 || deliveries[0].Agent.MemberID != "" {
		t.Fatalf("quarantined delivery changed identity: %+v, %v", deliveries, err)
	}
}

func TestBindWriteFailureLeavesThePreviousSeatBound(t *testing.T) {
	r := newRig(t)
	r.register("session", "boot")
	r.bind("claude-code", "session", reviewer)
	if err := r.journal.Close(); err != nil {
		t.Fatal(err)
	}
	resp := r.call(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "session", Agent: &planner})
	if resp.Error == nil || resp.Error.Code != "internal" {
		t.Fatalf("binding to a closed journal: %+v", resp)
	}
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "session"})
	if len(got.Agents) != 1 || got.Agents[0] != reviewer {
		t.Fatalf("failed write changed the active seat: %+v", got)
	}
}

func TestTemporaryOutageKeepsAnAlreadyVerifiedJournalSeat(t *testing.T) {
	r := newRig(t)
	ref := reviewer
	ref.MemberID = "mem_original"
	r.register("session", "boot")
	r.bind("claude-code", "session", ref)
	r.stop()
	r.resolve = func(context.Context, delivery.AgentRef) (delivery.AgentRef, error) {
		return delivery.AgentRef{}, errors.New("server unavailable")
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "session"})
	if len(got.Agents) != 1 || got.Agents[0] != ref {
		t.Fatalf("outage dropped verified binding: %+v", got)
	}
}
