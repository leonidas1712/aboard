package board_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// keyWorld is a service whose first person, an admin, holds a key that the test ends
// between authenticating a request and the request's write.
type keyWorld struct {
	svc   *board.Service
	clk   *clock.Fake
	path  string
	owner string // the admin's first key
	board string // a board the admin created
	agent string // the token of an agent the admin added to it
	gate  *gatedStore
}

// gatedStore is the real store, except that once armed its next write waits, before it
// starts its transaction, until the test lets it go: it plays a write queued behind
// another one for the write lock.
type gatedStore struct {
	board.Store
	mu      sync.Mutex
	waiting chan struct{} // closed when the gated write starts waiting
	release chan struct{} // closed by the test to let it go
}

// arm makes the next write wait, and returns channels for it.
func (g *gatedStore) arm() (waiting, release chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting, g.release = make(chan struct{}), make(chan struct{})
	return g.waiting, g.release
}

func (g *gatedStore) Write(ctx context.Context, fn func(board.Tx) error) error {
	g.mu.Lock()
	waiting, release := g.waiting, g.release
	g.waiting, g.release = nil, nil
	g.mu.Unlock()
	if waiting != nil {
		close(waiting)
		<-release
	}
	return g.Store.Write(ctx, fn)
}

var digestKey = []byte("test digest key")

func newKeyWorld(t *testing.T) *keyWorld {
	t.Helper()
	ctx := context.Background()
	w := &keyWorld{clk: clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)), path: filepath.Join(t.TempDir(), "aboard.db")}
	st, err := sqlite.Open(ctx, w.path, w.clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w.gate = &gatedStore{Store: st}
	w.svc = board.New(w.gate, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if w.owner, err = w.svc.BootstrapOwner(ctx, "alex", "laptop"); err != nil {
		t.Fatal(err)
	}
	p := w.auth(t, w.owner)
	v, err := w.svc.CreateBoard(ctx, p, board.NewBoard{Template: "writer-reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	w.board = v.Board.Name
	joined, err := w.svc.Join(ctx, p, board.JoinInput{Board: w.board, Role: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = joined.Token
	return w
}

func (w *keyWorld) auth(t *testing.T, token string) board.Principal {
	t.Helper()
	p, err := w.svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// sql changes the database directly, as an admin tool or a later key command would.
func (w *keyWorld) sql(t *testing.T, query string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+w.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), query); err != nil {
		t.Fatal(err)
	}
}

// writes are the writes a person's key or an agent's token can make, each run with
// principals authenticated before the key ended.
var writes = map[string]func(ctx context.Context, w *keyWorld, person, agent board.Principal) error{
	"invite": func(ctx context.Context, w *keyWorld, person, _ board.Principal) error {
		_, err := w.svc.CreateServerInvite(ctx, person, 0)
		return err
	},
	"create a board": func(ctx context.Context, w *keyWorld, person, _ board.Principal) error {
		_, err := w.svc.CreateBoard(ctx, person, board.NewBoard{Template: "general"})
		return err
	},
	"join": func(ctx context.Context, w *keyWorld, person, _ board.Principal) error {
		_, err := w.svc.Join(ctx, person, board.JoinInput{Board: w.board, Role: "reviewer"})
		return err
	},
	"log browsers out": func(ctx context.Context, w *keyWorld, person, _ board.Principal) error {
		_, err := w.svc.EndBrowserLogins(ctx, person)
		return err
	},
	"post as the person": func(ctx context.Context, w *keyWorld, person, _ board.Principal) error {
		_, err := w.svc.PostMessage(ctx, person, w.board, board.NewMessage{Body: "hi"})
		return err
	},
	"post as an agent of the key": func(ctx context.Context, w *keyWorld, _, agent board.Principal) error {
		_, err := w.svc.PostMessage(ctx, agent, w.board, board.NewMessage{Body: "hi"})
		return err
	},
}

func wantUnauthorized(t *testing.T, what string, err error) {
	t.Helper()
	if e, ok := apierr.As(err); !ok || e.Code != "unauthorized" {
		t.Fatalf("%s after the key ended: got %v, want unauthorized", what, err)
	}
}

// A key revoked after a request was authenticated can't write: the write rechecks the key
// inside its own transaction.
func TestARevokedKeyCantFinishAWrite(t *testing.T) {
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			w := newKeyWorld(t)
			person, agent := w.auth(t, w.owner), w.auth(t, w.agent)
			w.sql(t, "UPDATE access_keys SET revoked_at = '2026-10-01T16:00:00.000Z'")
			wantUnauthorized(t, name, write(context.Background(), w, person, agent))
		})
	}
}

// A key that expires while a write waits can't finish it.
func TestAKeyExpiringBeforeTheWriteCantFinishIt(t *testing.T) {
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			w := newKeyWorld(t)
			w.sql(t, "UPDATE access_keys SET expires_at = '2026-10-01T16:01:00.000Z'")
			person, agent := w.auth(t, w.owner), w.auth(t, w.agent)
			w.clk.Advance(2 * time.Minute)
			wantUnauthorized(t, name, write(context.Background(), w, person, agent))
		})
	}
}

// A login code whose key ended before the browser used it gives no browser login.
func TestALoginCodeDiesWithItsKey(t *testing.T) {
	w := newKeyWorld(t)
	code, err := w.svc.CreateLoginCode(context.Background(), w.auth(t, w.owner))
	if err != nil {
		t.Fatal(err)
	}
	w.sql(t, "UPDATE access_keys SET revoked_at = '2026-10-01T16:00:00.000Z'")
	_, _, err = w.svc.CreateBrowserToken(context.Background(), code.Code)
	if e, ok := apierr.As(err); !ok || e.Code != "login_code_invalid" {
		t.Fatalf("exchanging the code: got %v, want login_code_invalid", err)
	}
}

// queued runs call while its write waits for the write lock, moving the clock by d in
// that wait, and returns call's error.
func (w *keyWorld) queued(d time.Duration, call func() error) error {
	waiting, release := w.gate.arm()
	done := make(chan error, 1)
	go func() { done <- call() }()
	<-waiting
	w.clk.Advance(d)
	close(release)
	return <-done
}

// A login code, or the key that asked for it, ending while the exchange waits for the
// write lock gives no browser login: the exchange reads the time inside its transaction.
func TestALoginCodeEndingWhileTheExchangeWaitsFails(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup string
		wait  time.Duration
	}{
		{"the code expires", "", board.LoginCodeTTL + time.Second},
		{"the key expires", "UPDATE access_keys SET expires_at = '2026-10-01T16:00:30.000Z'", 40 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newKeyWorld(t)
			if tt.setup != "" {
				w.sql(t, tt.setup)
			}
			code, err := w.svc.CreateLoginCode(context.Background(), w.auth(t, w.owner))
			if err != nil {
				t.Fatal(err)
			}
			err = w.queued(tt.wait, func() error {
				_, _, err := w.svc.CreateBrowserToken(context.Background(), code.Code)
				return err
			})
			if e, ok := apierr.As(err); !ok || e.Code != "login_code_invalid" {
				t.Fatalf("got %v, want login_code_invalid", err)
			}
		})
	}
}

// A key expiring while a write waits for the write lock can't finish it.
func TestAKeyExpiringWhileTheWriteWaitsCantFinishIt(t *testing.T) {
	w := newKeyWorld(t)
	w.sql(t, "UPDATE access_keys SET expires_at = '2026-10-01T16:00:30.000Z'")
	person := w.auth(t, w.owner)
	err := w.queued(40*time.Second, func() error {
		_, err := w.svc.CreateServerInvite(context.Background(), person, 0)
		return err
	})
	wantUnauthorized(t, "invite", err)
}

// A display name is limited to 80 characters, not bytes.
func TestDisplayNamesCountCharacters(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	for _, tt := range []struct {
		display string
		ok      bool
	}{
		{strings.Repeat("陈", 80), true},
		{strings.Repeat("é", 80), true},
		{strings.Repeat("陈", 81), false},
	} {
		inv, err := w.svc.CreateServerInvite(ctx, w.auth(t, w.owner), 0)
		if err != nil {
			t.Fatal(err)
		}
		c, err := w.svc.Connect(ctx, board.ConnectInput{Invite: inv.Secret, Handle: "p" + strings.ToLower(inv.Invite.ID[len(inv.Invite.ID)-6:]), DisplayName: tt.display, KeyName: "laptop"})
		if tt.ok && (err != nil || *c.Person.DisplayName != tt.display) {
			t.Fatalf("%d runes: %v", len([]rune(tt.display)), err)
		}
		if !tt.ok {
			if e, ok := apierr.As(err); !ok || e.Code != "invalid_request" {
				t.Fatalf("81 characters: got %v, want invalid_request", err)
			}
		}
	}
}
