package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestRepliesWaitingRecheckThePersonsAuthorityInsideTheRead(t *testing.T) {
	for _, how := range []string{"revoked", "expired", "removed"} {
		t.Run(how, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			person, _, keyID := w.shortKey(ctx, t)
			_, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{To: []string{"@sam"}, Body: "Please reply", ExpectsReply: true})
			if err != nil {
				t.Fatal(err)
			}
			before, err := w.svc.RepliesWaiting(ctx, person, board.RepliesWaitingFilter{})
			if err != nil || before.Count != 1 {
				t.Fatalf("before: %+v %v", before, err)
			}
			var got board.RepliesWaitingListing
			err = w.gatedRead(t, func() error {
				var err error
				got, err = w.svc.RepliesWaiting(ctx, person, board.RepliesWaitingFilter{})
				return err
			}, func() {
				switch how {
				case "expired":
					w.clk.Advance(board.MinKeyTTL + time.Minute)
				case "revoked":
					if _, err := w.svc.RevokeKey(ctx, w.sam, keyID); err != nil {
						t.Error(err)
					}
				case "removed":
					if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
						t.Error(err)
					}
				}
			})
			if how != "removed" {
				wantCode(t, "replies waiting", err, "unauthorized")
			} else if err != nil {
				t.Fatal(err)
			}
			if got.Count != 0 || len(got.Requests) != 0 || got.More {
				t.Fatalf("authority ended but payload leaked: %+v", got)
			}
		})
	}
}
