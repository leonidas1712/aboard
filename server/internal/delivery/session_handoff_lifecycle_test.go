package delivery_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func bindLifecycleSeats(t *testing.T, r *rig, session string) (a, b delivery.AgentRef) {
	t.Helper()
	a, b = reviewer, planner
	a.MemberID, b.MemberID = "mem_lifecycle_docs", "mem_lifecycle_plans"
	r.bind("codex", session, a)
	r.bind("codex", session, b)
	if refs := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: session}).Agents; len(refs) != 2 {
		t.Fatalf("lifecycle fixture requires two live seats: %+v", refs)
	}
	return a, b
}

func TestRemovingOneSeatDuringCoalescingKeepsItsSiblingAlive(t *testing.T) {
	r := newRig(t)
	a, b := bindLifecycleSeats(t, r, "coalescing")
	r.postFromOwner(a, "removed before handoff")
	r.postFromOwner(b, "surviving decision")
	r.server.TakeOff(a)
	// No clock advance admits the bundle until the refusal has reached the mailbox.
	r.postFromOwner(a, "revocation head")
	r.eventually("removed seat", 0, func() bool {
		for _, p := range r.status().Agents {
			if p.Agent.Key() == a.Key() && p.Reason == delivery.ReasonBoardGone {
				return true
			}
		}
		return false
	})
	r.eventually("surviving acknowledgment", 500*time.Millisecond, func() bool { return r.server.Cursor(b) == 1 })
	handed := r.codex.Handed("coalescing")
	if len(handed) != 1 || strings.Contains(handed[0], "removed before") || !strings.Contains(handed[0], "surviving decision") || r.server.Cursor(a) != 0 {
		t.Fatalf("seat refusal damaged or leaked sibling delivery: %+v; cursors %d/%d", handed, r.server.Cursor(a), r.server.Cursor(b))
	}
	r.postFromOwner(b, "sibling still running")
	r.eventually("sibling continuation", 500*time.Millisecond, func() bool { return r.server.Cursor(b) == 2 })
}

func TestRemovalAfterCombinedAcceptanceDoesNotRepeatTheSurvivingBlock(t *testing.T) {
	r := newRig(t)
	a, b := bindLifecycleSeats(t, r, "accepted")
	var once sync.Once
	r.codex.OnHand(func() { once.Do(func() { r.server.TakeOff(a) }) })
	r.postFromOwner(a, "accepted before removal")
	r.postFromOwner(b, "accepted surviving decision")
	r.eventually("surviving acknowledgment", 500*time.Millisecond, func() bool { return r.server.Cursor(b) == 1 })
	r.eventually("removed seat terminal", 0, func() bool {
		for _, p := range r.status().Agents {
			if p.Agent.Key() == a.Key() && p.Reason == delivery.ReasonBoardGone {
				return true
			}
		}
		return false
	})
	handed := r.codex.Handed("accepted")
	if len(handed) != 1 || !strings.Contains(handed[0], "accepted before removal") || !strings.Contains(handed[0], "accepted surviving decision") || r.server.Cursor(a) != 0 {
		t.Fatalf("acceptance/removal result: %+v; cursors %d/%d", handed, r.server.Cursor(a), r.server.Cursor(b))
	}
	r.restart()
	r.postFromOwner(b, "after restart")
	r.eventually("post-restart sibling", 500*time.Millisecond, func() bool { return r.server.Cursor(b) == 2 })
	handed = r.codex.Handed("accepted")
	if len(handed) != 2 || strings.Contains(handed[1], "accepted surviving decision") {
		t.Fatalf("accepted sibling block repeated: %+v", handed)
	}
}

type lifecycleAckServer struct {
	delivery.Server
	seats    *fakeSeats
	mu       sync.Mutex
	tokens   []string
	accepted []string
}

func (s *lifecycleAckServer) Ack(ctx context.Context, a delivery.AgentRef, seq int) error {
	s.seats.mu.Lock()
	token := s.seats.saved[a.MemberID]
	s.seats.mu.Unlock()
	s.mu.Lock()
	s.tokens = append(s.tokens, token)
	s.mu.Unlock()
	err := s.Server.Ack(ctx, a, seq)
	if err == nil {
		s.mu.Lock()
		s.accepted = append(s.accepted, token)
		s.mu.Unlock()
	}
	return err
}

func (s *lifecycleAckServer) used(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.tokens {
		if v == token {
			return true
		}
	}
	return false
}

func (s *lifecycleAckServer) acceptedWith(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.accepted {
		if v == token {
			return true
		}
	}
	return false
}

func TestConfirmedHandoffSurvivesCredentialRotationBeforeAckAndRestart(t *testing.T) {
	r, f := seatsRig(t)
	r.status()
	r.stop()
	srv := &lifecycleAckServer{Server: r.server, seats: f}
	r.remote = srv
	r.start()
	request := delivery.Request{Op: delivery.OpJoin, Harness: "codex", Session: "rotate-confirmed", Agent: &delivery.AgentRef{Server: serverURL, Board: "docs"}}
	first := r.ok(request)
	a := delivery.AgentRef{Server: first.Joined.Server, Board: first.Joined.Board, Name: first.Joined.Name, MemberID: first.Joined.MemberID}
	r.server.FailAcks(true)
	r.postFromOwner(a, "already received")
	r.eventually("durable confirmation", 500*time.Millisecond, func() bool {
		rows, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
		return err == nil && len(rows) == 1 && srv.used("aba_token-1")
	})
	rotated := r.ok(request)
	if !rotated.Reused || rotated.Joined.MemberID != a.MemberID {
		t.Fatalf("rotation minted another identity: %+v", rotated)
	}
	r.eventually("new token ack attempt", 0, func() bool { return srv.used("aba_token-2") })
	r.restart()
	r.server.FailAcks(false)
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "rotate-confirmed"})
	r.eventually("fresh token acknowledgment", 500*time.Millisecond, func() bool { return r.server.Cursor(a) == 1 })
	if len(r.codex.Handed("rotate-confirmed")) != 1 || !srv.acceptedWith("aba_token-2") {
		t.Fatal("credential rotation repeated already received text or acknowledged with old credentials")
	}
}

type lifecycleGatedAdapter struct {
	delivery.Adapter
	entered chan delivery.Handover
	release chan struct{}
}

func (a *lifecycleGatedAdapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	confirmed, err := a.Adapter.Hand(ctx, h)
	if err != nil {
		return confirmed, err
	}
	select {
	case a.entered <- h:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	select {
	case <-a.release:
		return confirmed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func nextLifecycleHandoff(t *testing.T, r *rig, a *lifecycleGatedAdapter) delivery.Handover {
	t.Helper()
	var h delivery.Handover
	r.eventually("adapter handoff", 500*time.Millisecond, func() bool {
		select {
		case h = <-a.entered:
			return true
		default:
			return false
		}
	})
	return h
}

func TestOldSameBootAdapterConfirmationCannotConfirmANewBinding(t *testing.T) {
	r := newRig(t)
	r.status()
	r.stop()
	gated := &lifecycleGatedAdapter{Adapter: r.codex, entered: make(chan delivery.Handover, 2), release: make(chan struct{}, 2)}
	r.configure = func(cfg *delivery.Config) { cfg.Adapters[1] = gated }
	r.start()
	a := reviewer
	a.MemberID = "mem_rebind"
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "old", Boot: "shared-boot"})
	r.bind("codex", "old", a)
	r.postFromOwner(a, "accepted by old binding")
	old := nextLifecycleHandoff(t, r, gated)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "new", Boot: "shared-boot"})
	r.bind("codex", "new", a)
	gated.release <- struct{}{}
	var fresh delivery.Handover
	r.eventually("new binding handoff", 500*time.Millisecond, func() bool {
		if r.server.Cursor(a) != 0 {
			t.Fatal("old adapter callback acknowledged the new binding")
		}
		select {
		case fresh = <-gated.entered:
			return true
		default:
			return false
		}
	})
	rows, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
	if err != nil || len(rows) != 0 || r.server.Cursor(a) != 0 {
		t.Fatalf("stale callback confirmed new binding: %+v, cursor=%d, err=%v", rows, r.server.Cursor(a), err)
	}
	manifests, err := r.journal.Handoffs(context.Background())
	if err != nil || len(manifests) != 2 || old.SessionID == fresh.SessionID {
		t.Fatalf("rebind did not create a fresh immutable handoff: %+v, %v", manifests, err)
	}
	generations := map[string]uint64{}
	for _, m := range manifests {
		generations[m.Session.ID] = m.Parts[0].Generation
	}
	if generations["new"] <= generations["old"] {
		t.Fatalf("binding generation did not advance: %+v", generations)
	}
	gated.release <- struct{}{}
	r.eventually("new binding confirms", 0, func() bool { return r.server.Cursor(a) == 1 })
}
