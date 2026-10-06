package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// toSam posts a message from maya to sam and returns its seq.
func (w *teamWorld) toSam(ctx context.Context, t *testing.T) int64 {
	t.Helper()
	m, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{To: []string{"@sam"}, Body: "for sam"})
	if err != nil {
		t.Fatal(err)
	}
	return m.Seq
}

// A key revoked, or expiring, while a read of receipts or of read positions waits for
// its transaction reads nothing: each checks the credential, with the time, inside the
// transaction that reads.
func TestAKeyEndingWhileAnAttentionReadWaitsEndsTheRead(t *testing.T) {
	attention := map[string]func(ctx context.Context, w *teamWorld, p board.Principal, seq int64) error{
		"receipts": func(ctx context.Context, w *teamWorld, p board.Principal, seq int64) error {
			_, err := w.svc.Receipts(ctx, p, w.board, seq)
			return err
		},
		"unread on the board": func(ctx context.Context, w *teamWorld, p board.Principal, _ int64) error {
			_, err := w.svc.GetBoard(ctx, p, w.board)
			return err
		},
		"unread in the list": func(ctx context.Context, w *teamWorld, p board.Principal, _ int64) error {
			_, err := w.svc.ListBoards(ctx, p, false)
			return err
		},
	}
	for name, read := range attention {
		for _, who := range []string{"person", "agent"} {
			for _, how := range []string{"revoked", "expired"} {
				t.Run(name+" by the "+who+", key "+how, func(t *testing.T) {
					w := newTeamWorld(t)
					ctx := context.Background()
					person, agent, keyID := w.shortKey(ctx, t)
					seq := w.toSam(ctx, t)
					p := person
					if who == "agent" {
						p = agent
					}
					if err := read(ctx, w, p, seq); err != nil {
						t.Fatalf("before the key ended: %v", err)
					}
					err := w.gatedRead(t, func() error { return read(ctx, w, p, seq) }, func() {
						if how == "expired" {
							w.clk.Advance(board.MinKeyTTL + time.Minute)
							return
						}
						if _, err := w.svc.RevokeKey(ctx, w.sam, keyID); err != nil {
							t.Error(err)
						}
					})
					wantCode(t, name, err, "unauthorized")
				})
			}
		}
	}
}

// A person removed from a private board while a read of a message's receipts waits
// reads nothing, nor does their agent; and a count of their unread messages read in that
// wait no longer includes the board.
func TestRemovalWhileAnAttentionReadWaitsEndsTheRead(t *testing.T) {
	for _, who := range []string{"person", "agent"} {
		t.Run("receipts by the "+who, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			seq := w.toSam(ctx, t)
			p := w.sam
			if who == "agent" {
				p = w.samAgent
			}
			if _, err := w.svc.Receipts(ctx, p, w.board, seq); err != nil {
				t.Fatalf("before removal: %v", err)
			}
			err := w.gatedRead(t, func() error { _, err := w.svc.Receipts(ctx, p, w.board, seq); return err }, func() {
				if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
					t.Error(err)
				}
			})
			want := "board_not_found"
			if who == "agent" {
				want = "agent_removed"
			}
			wantCode(t, "receipts", err, want)
		})
	}
	t.Run("unread in the list", func(t *testing.T) {
		w := newTeamWorld(t)
		ctx := context.Background()
		w.toSam(ctx, t)
		var l board.Listing
		err := w.gatedRead(t, func() error {
			var err error
			l, err = w.svc.ListBoards(ctx, w.sam, true)
			return err
		}, func() {
			if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
				t.Error(err)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range l.Boards {
			if v.Board.Name == w.board {
				t.Fatalf("the list still had the private board after removal: %+v", v)
			}
		}
	})
}

// A person's stream reads their read position on each board inside the transaction that
// checks they are still on it: removed while that read waits, they get no position for
// the board; their key revoked in that wait, the stream ends.
func TestTheStreamsUnreadCountsFollowAccess(t *testing.T) {
	for _, change := range []string{"removed", "revoked"} {
		t.Run(change, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			person, _, keyID := w.shortKey(ctx, t)
			feed, err := w.svc.FollowHeads(person)
			if err != nil {
				t.Fatal(err)
			}
			tick := make(chan time.Time, 1)
			first, _, err := feed.Next(ctx, tick)
			if err != nil || len(first.Unread) != 1 || first.Unread[0].Board != w.board {
				t.Fatalf("first update: %+v %v", first, err)
			}
			w.toSam(ctx, t)
			// The next update reads the credential, then the heads, then the positions:
			// the third read waits while sam loses the board or the key.
			waiting, release := w.gate.armAfter(2)
			type result struct {
				u   board.Update
				err error
			}
			done := make(chan result, 1)
			go func() {
				u, _, err := feed.Next(ctx, tick)
				done <- result{u, err}
			}()
			<-waiting
			if change == "removed" {
				if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := w.svc.RevokeKey(ctx, w.sam, keyID); err != nil {
				t.Fatal(err)
			}
			close(release)
			tick <- time.Now()
			var r result
			select {
			case r = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the stream's update never came")
			}
			if change == "revoked" {
				wantCode(t, "the stream", r.err, "unauthorized")
				return
			}
			if r.err != nil {
				t.Fatal(r.err)
			}
			for _, uc := range r.u.Unread {
				if uc.Board == w.board {
					t.Fatalf("the stream sent an unread count for the board after removal: %+v", uc)
				}
			}
		})
	}
}
