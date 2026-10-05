package board_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

// readGate is the real store, except that its nth read waits, before its transaction
// starts, until the test lets it go: it plays a read that a credential's end overtakes.
type readGate struct {
	board.Store
	mu      sync.Mutex
	n, at   int
	waiting chan struct{}
	release chan struct{}
}

func (g *readGate) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	g.mu.Lock()
	g.n++
	hold := g.n == g.at
	g.mu.Unlock()
	if hold {
		close(g.waiting)
		<-g.release
	}
	return g.Store.Read(ctx, fn)
}

// gated returns a service on w's store whose nth read waits for the test.
func (w *keyWorld) gated(nth int) (*board.Service, *readGate) {
	g := &readGate{Store: w.gate.Store, at: nth, waiting: make(chan struct{}), release: make(chan struct{})}
	return board.New(g, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey, w.svc.Config(),
		slog.New(slog.NewTextHandler(io.Discard, nil))), g
}

// revokedWhileReading runs read on a service whose content read waits; meanwhile the
// owner's key is revoked and a message is posted with another working key. read must
// fail: content is read only with a credential checked in the same transaction.
func revokedWhileReading(t *testing.T, nth int, read func(svc *board.Service, person, agent board.Principal, root string) error) {
	t.Helper()
	w := newKeyWorld(t)
	ctx := context.Background()
	person, agent := w.auth(t, w.owner), w.auth(t, w.agent)
	spare, err := w.svc.CreateKey(ctx, person, "spare", 0)
	if err != nil {
		t.Fatal(err)
	}
	other := w.auth(t, spare.Token)
	root, err := w.svc.PostMessage(ctx, person, w.board, board.NewMessage{Body: "the thread"})
	if err != nil {
		t.Fatal(err)
	}
	svc, g := w.gated(nth)
	done := make(chan error, 1)
	go func() { done <- read(svc, person, agent, root.ID) }()
	<-g.waiting
	w.sql(t, "UPDATE access_keys SET revoked_at = '2026-10-01T16:00:00.000Z' WHERE id = '"+person.KeyID+"'")
	if _, err := w.svc.PostMessage(ctx, other, w.board, board.NewMessage{Body: "after the revocation", ReplyTo: &root.ID, To: []string{"all"}}); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err == nil {
		t.Fatal("a read whose credential ended while it waited for the store returned content")
	}
}

func TestARevokedInboxWaitReadsNothingNew(t *testing.T) {
	revokedWhileReading(t, 2, func(svc *board.Service, _, agent board.Principal, _ string) error {
		_, _, err := svc.Inbox(context.Background(), agent, time.Minute, 0, 10)
		return err
	})
}

func TestARevokedThreadWaitReadsNothingNew(t *testing.T) {
	revokedWhileReading(t, 2, func(svc *board.Service, _, agent board.Principal, root string) error {
		_, err := svc.Thread(context.Background(), agent, root, time.Minute, 0, 10)
		return err
	})
}

func TestARevokedStreamReadsNothingNew(t *testing.T) {
	revokedWhileReading(t, 2, func(svc *board.Service, person, _ board.Principal, _ string) error {
		feed, err := svc.FollowHeads(person)
		if err != nil {
			return err
		}
		_, _, err = feed.Next(context.Background(), nil)
		return err
	})
}

// A key that expires while its authentication waits for the store is refused: the time
// is read inside the authentication's transaction.
func TestAKeyExpiringDuringAuthenticationIsRefused(t *testing.T) {
	w := newKeyWorld(t)
	w.sql(t, "UPDATE access_keys SET expires_at = '2026-10-01T16:00:30.000Z'")
	svc, g := w.gated(1)
	done := make(chan error, 1)
	go func() {
		_, err := svc.Authenticate(context.Background(), w.owner)
		done <- err
	}()
	<-g.waiting
	w.clk.Advance(40 * time.Second)
	close(g.release)
	if err := <-done; err == nil {
		t.Fatal("a key that expired while it was being checked was accepted")
	}
}
