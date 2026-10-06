package board_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// An agent removed while one of its reads waits for its transaction reads nothing: the
// read checks the seat inside its own transaction and says the agent was removed.
func TestAgentRemovalWhileItsReadWaitsEndsTheRead(t *testing.T) {
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			if err := read(ctx, w, w.samAgent); err != nil {
				t.Fatalf("before removal: %v", err)
			}
			err := w.gatedRead(t, func() error { return read(ctx, w, w.samAgent) }, func() {
				if _, err := w.svc.RemoveAgent(ctx, w.maya, w.board, w.samAgent.Agent.Name); err != nil {
					t.Error(err)
				}
			})
			wantCode(t, name, err, "agent_removed")
		})
	}
}

// The join codes an agent made for its board stop with it, each recorded.
func TestAgentRemovalStopsTheCodesItMade(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	me, err := w.svc.WhoAmI(ctx, w.samAgent)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.svc.GetBoard(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	err = w.gate.Write(ctx, func(tx board.Tx) error {
		return tx.InsertJoinCode(board.JoinCode{
			ID: "jc_agents", BoardID: b.Board.ID, CodeDigest: "agents-code", Role: "member",
			ExpiresAt: "2026-10-02T16:00:00.000Z", CreatedAt: "2026-10-01T16:00:00.000Z", CreatedBy: me.Agent.ID, Kind: board.CodePairing,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.RemoveAgent(ctx, w.sam, w.board, me.Agent.ID); err != nil {
		t.Fatal(err)
	}
	log, err := w.svc.Events(ctx, w.maya, w.board, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	last := log.Events[len(log.Events)-1]
	if last.Type != "joincode.revoked" || log.Events[len(log.Events)-2].Type != "agent.removed" {
		t.Fatalf("the removal's events end with %s", last.Type)
	}
	err = w.gate.Read(ctx, func(tx board.ReadTx) error {
		jc, err := tx.JoinCodeByID("jc_agents")
		if err == nil && jc.RevokedAt == nil {
			t.Error("the removed agent's join code still works")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
