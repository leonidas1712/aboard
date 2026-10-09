package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestLogicalPeerTurnSurvivesRegisterWaitAndDaemonRestart(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "logical-peer-turn", Boot: "b1"}
	}
	current := func() delivery.SessionRecord {
		t.Helper()
		records, err := r.journal.Sessions(context.Background())
		if err != nil || len(records) != 1 {
			t.Fatalf("session records: %+v %v", records, err)
		}
		return records[0]
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "logical-peer-turn", reviewer)
	r.ok(req(delivery.OpPrompt))
	first := current()
	if !first.PeerTurnActive || first.PeerTurn != 1 {
		t.Fatalf("first logical turn: %+v", first)
	}
	r.ok(req(delivery.OpRegister))
	r.ok(req(delivery.OpBoundary))
	if got := current(); !got.PeerTurnActive || got.PeerTurn != first.PeerTurn {
		t.Fatalf("register replenished the allowance: %+v", got)
	}
	_ = r.wait("logical-peer-turn", "b1", false, "codex")
	if got := current(); !got.PeerTurnActive {
		t.Fatalf("Stop waiting closed the logical turn: %+v", got)
	}
	r.stop()
	r.start()
	r.ok(req(delivery.OpPrompt))
	if got := current(); !got.PeerTurnActive || got.PeerTurn != first.PeerTurn {
		t.Fatalf("restart/continuation replenished the allowance: %+v", got)
	}
	r.clock.Advance(time.Second)
	r.ok(req(delivery.OpBoundary))
	r.stop()
	r.start()
	stale := req(delivery.OpTurnEnd)
	stale.Started = r.clock.Now().Add(-time.Second)
	r.ok(stale)
	if got := current(); !got.PeerTurnActive {
		t.Fatalf("stale completion closed the current turn: %+v", got)
	}
	complete := req(delivery.OpTurnEnd)
	complete.TurnID = first.PeerTurn + 1
	r.ok(complete)
	if got := current(); !got.PeerTurnActive {
		t.Fatalf("another turn's completion closed the current turn: %+v", got)
	}
	complete.TurnID = first.PeerTurn
	complete.Started = r.clock.Now()
	r.ok(complete)
	r.ok(req(delivery.OpPrompt))
	if got := current(); !got.PeerTurnActive || got.PeerTurn != first.PeerTurn+1 {
		t.Fatalf("completed turn did not replenish once: %+v", got)
	}
}
