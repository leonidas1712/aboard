package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestAgentAddChecksCurrentAuthorityWhenItsWriteStarts(t *testing.T) {
	for _, which := range []string{"server gate", "board gate", "owner removed", "parent expiry"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			w := newTeamWorld(t)
			yes, no := true, false
			if _, err := w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{AgentsAddPeople: &yes}); err != nil {
				t.Fatal(err)
			}
			delegation, err := w.svc.CreateDelegation(ctx, w.sam, "teamwork")
			if err != nil {
				t.Fatal(err)
			}
			joined, err := w.svc.Join(ctx, w.auth(ctx, t, delegation.Token), board.JoinInput{Board: w.board, Session: "codex:gated-add", Harness: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			agent := w.auth(ctx, t, joined.Token)
			waiting, release := w.gate.armWrite()
			done := make(chan error, 1)
			go func() { _, err := w.svc.AddPerson(ctx, agent, w.board, "alex"); done <- err }()
			<-waiting
			code := "add_people_not_allowed"
			switch which {
			case "server gate":
				_, err = w.svc.UpdateServerSettings(ctx, w.alex, board.Settings{AgentsAddPeople: &no})
			case "board gate":
				_, err = w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{AgentsAddPeople: &no})
			case "owner removed":
				_, err = w.svc.RemovePerson(ctx, w.maya, w.board, "sam")
				code = "agent_removed"
			case "parent expiry":
				w.clk.Advance(91 * 24 * time.Hour)
				code = "unauthorized"
			}
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			var head int64
			if err := w.gate.Store.Read(ctx, func(tx board.ReadTx) error { b, err := tx.BoardByName(w.board); head = b.HeadSeq; return err }); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			e, ok := apierr.As(<-done)
			if !ok || e.Code != code {
				t.Fatalf("add after %s: %+v, want %s", which, e, code)
			}
			if err := w.gate.Store.Read(ctx, func(tx board.ReadTx) error {
				b, err := tx.BoardByName(w.board)
				if b.HeadSeq != head {
					t.Errorf("refused add appended an event: %d -> %d", head, b.HeadSeq)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
