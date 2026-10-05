package delivery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// stalledSeqs returns the messages of the deliveries the daemon reports as stalled.
func (r *rig) stalledSeqs() []int {
	r.t.Helper()
	var seqs []int
	for _, item := range r.status().Stalled {
		if item.Reason != delivery.ReasonNoTurn {
			r.t.Fatalf("a stalled delivery's reason is %q", item.Reason)
		}
		seqs = append(seqs, item.Seqs...)
	}
	return seqs
}

// A bundle a waiting hook took wakes the session; when no turn starts within
// StallAfter, the delivery is reported as stalled, and the turn starting clears it. It is
// never handed again because of it.
func TestWakeThatStartsNoTurnIsReportedAsStalled(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	seq := r.post(reviewer, "anyone home?", false)
	r.wait("s1", "b1", false).bundle()

	r.eventually("the delivery to stall", time.Second, func() bool { return len(r.stalledSeqs()) == 1 })
	if got := r.stalledSeqs(); got[0] != seq {
		t.Fatalf("stalled %v, want #%d", got, seq)
	}
	if n := len(r.claude.Handed("s1")); n != 1 {
		t.Fatalf("a stalled delivery was handed %d times", n)
	}
	r.hookCall(delivery.OpPrompt)
	r.eventually("the stall to clear", 0, func() bool { return len(r.stalledSeqs()) == 0 })
}

// A woken session that starts its turn in time never stalls, and the journal keeps when
// the harness accepted the delivery and when the turn started.
func TestWakeThatStartsATurnRecordsBothStages(t *testing.T) {
	server := &ackHeldAfterCommit{committed: make(chan struct{}), release: make(chan struct{})}
	r := newRigWithServer(t, func(base delivery.Server) delivery.Server {
		server.Server = base
		return server
	})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(server.release) }) })
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	seq := r.post(reviewer, "please review", false)
	r.wait("s1", "b1", false).bundle()
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "claude-code", Session: "s1", Boot: "b1", Wake: true})
	r.wait("s1", "b1", false) // the turn ends: confirms
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
	r.clock.Advance(time.Minute)
	if got := r.stalledSeqs(); len(got) != 0 {
		t.Fatalf("stalled %v though the turn started", got)
	}
	// The server committed the cursor, but the daemon has not received its answer.
	// A remote acknowledgement is not a barrier for the local journal's completion.
	_, _ = await(t, nil, server.committed, "the remote acknowledgement")
	confirmed, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 1 {
		t.Fatalf("before the acknowledgement response, confirmed deliveries: %+v", confirmed)
	}
	release.Do(func() { close(server.release) })
	var ds []delivery.Delivery
	r.eventually("the journal's completion", 0, func() bool {
		var err error
		ds, err = r.journal.Deliveries(context.Background(), delivery.StateDone)
		if err != nil {
			t.Fatal(err)
		}
		return len(ds) == 1
	})
	if len(ds) != 1 || ds[0].AcceptedAt.IsZero() || ds[0].TurnStartedAt.IsZero() || ds[0].TurnStartedAt.Before(ds[0].AcceptedAt) || ds[0].Stalled {
		t.Fatalf("the journal's record: %+v", ds)
	}
}

// A Codex thread whose hooks never ran (untrusted) reports no turns, so its deliveries
// can't be said to stall.
func TestSessionWhoseTurnsAreNeverSeenDoesntStall(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.post(reviewer, "queued", false)
	r.eventually("the queue to take it", delivery.QueueGather, func() bool { return len(r.codex.Handed("t1")) == 1 })
	r.clock.Advance(time.Minute)
	if got := r.stalledSeqs(); len(got) != 0 {
		t.Fatalf("stalled %v for a session whose turns the daemon never sees", got)
	}
}

// An extension that added a bundle to its idle session and confirmed it, with no turn
// starting after, stalls the same way; a turn starting clears it.
func TestExtensionBundleThatStartsNoTurnIsReportedAsStalled(t *testing.T) {
	r := newRig(t)
	e := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	seq := r.post(reviewer, "wake up", false)
	e.deliver(true)
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
	r.eventually("the delivery to stall", time.Second, func() bool { return len(r.stalledSeqs()) == 1 })
	e.send(delivery.Request{Op: delivery.OpPrompt})
	r.eventually("the stall to clear", 0, func() bool { return len(r.stalledSeqs()) == 0 })
}

// ackHeldAfterCommit separates the remote cursor change from its response reaching
// the daemon, as network delay can do after the server commits an acknowledgement.
type ackHeldAfterCommit struct {
	delivery.Server
	committed chan struct{}
	release   chan struct{}
}

func (s *ackHeldAfterCommit) Ack(ctx context.Context, agent delivery.AgentRef, upTo int) error {
	if err := s.Server.Ack(ctx, agent, upTo); err != nil {
		return err
	}
	close(s.committed)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
