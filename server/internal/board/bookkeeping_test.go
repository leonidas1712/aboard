package board_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

type bookkeepingGate struct {
	board.Store
	waiting chan struct{}
	release chan struct{}
}

func (g *bookkeepingGate) WriteBookkeeping(ctx context.Context, fn func(board.Tx) error) error {
	close(g.waiting)
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	st, ok := g.Store.(board.BookkeepingStore)
	if !ok {
		return errors.New("real store does not support bookkeeping groups")
	}
	return st.WriteBookkeeping(ctx, fn)
}

func TestQueuedBookkeepingRechecksTheCallingCredential(t *testing.T) {
	for _, presence := range []bool{false, true} {
		name := "acknowledgement"
		if presence {
			name = "presence"
		}
		t.Run(name, func(t *testing.T) {
			w := newKeyWorld(t)
			g := &bookkeepingGate{Store: w.gate.Store, waiting: make(chan struct{}), release: make(chan struct{})}
			w.svc = board.New(g, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey,
				board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			p := w.auth(t, w.agent)
			done := make(chan error, 1)
			go func() {
				if presence {
					_, _, err := w.svc.SetPresence(t.Context(), p, "working", "")
					done <- err
				} else {
					_, err := w.svc.Ack(t.Context(), p, 1)
					done <- err
				}
			}()
			<-g.waiting
			w.sql(t, "UPDATE access_keys SET revoked_at = '2026-10-01T16:00:00.000Z'")
			close(g.release)
			wantUnauthorized(t, name, <-done)
		})
	}
}

func TestFailedBookkeepingCommitNeitherMovesCursorNorNotifies(t *testing.T) {
	w := newKeyWorld(t)
	n := notify.NewInProcess()
	w.svc = board.New(w.gate.Store, n, w.clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p := w.auth(t, w.agent)
	msg, err := w.svc.PostMessage(t.Context(), w.auth(t, w.owner), w.board, board.NewMessage{Body: "Please read this."})
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := w.gate.Read(t.Context(), func(tx board.ReadTx) error {
		me, err := tx.MemberByName(p.Agent.BoardID, p.Agent.Name)
		before = me.Cursor
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.sql(t, `CREATE TABLE bookkeeping_invalid (human_id TEXT REFERENCES humans(id) DEFERRABLE INITIALLY DEFERRED)`)
	w.sql(t, `CREATE TRIGGER refuse_cursor_commit AFTER UPDATE OF cursor ON members BEGIN
		INSERT INTO bookkeeping_invalid VALUES ('missing_person'); END`)
	watch := n.Watch("read/" + p.Agent.BoardID + "/" + p.Agent.HumanID)
	if _, err := w.svc.Ack(t.Context(), p, msg.Seq); err == nil {
		t.Fatal("failed cursor commit reported success")
	}
	select {
	case <-watch:
		t.Fatal("failed acknowledgement notified the owner's stream")
	default:
	}
	if err := w.gate.Read(t.Context(), func(tx board.ReadTx) error {
		me, err := tx.MemberByName(p.Agent.BoardID, p.Agent.Name)
		if me.Cursor != before {
			t.Errorf("failed acknowledgement moved cursor to %d", me.Cursor)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
