package board_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"path/filepath"
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

// accessGate is the real store, except that once armed its next read waits, before it
// starts its transaction, until the test lets it go; and every finished read is
// signaled on reads, so a test knows when a long poll has read and is waiting.
type accessGate struct {
	board.Store
	mu      sync.Mutex
	waiting chan struct{}
	release chan struct{}
	reads   chan struct{}
}

func (g *accessGate) arm() (waiting, release chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting, g.release = make(chan struct{}), make(chan struct{})
	return g.waiting, g.release
}

func (g *accessGate) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	g.mu.Lock()
	waiting, release, reads := g.waiting, g.release, g.reads
	g.waiting, g.release = nil, nil
	g.mu.Unlock()
	if waiting != nil {
		close(waiting)
		<-release
	}
	err := g.Store.Read(ctx, fn)
	if reads != nil {
		select {
		case reads <- struct{}{}:
		default:
		}
	}
	return err
}

// teamWorld is a server with an admin, alex, and two members, maya and sam, and a
// private board of maya's that sam is on with an agent.
type teamWorld struct {
	svc             *board.Service
	gate            *accessGate
	alex, maya, sam board.Principal
	samAgent        board.Principal
	board           string
}

func newTeamWorld(t *testing.T) *teamWorld {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w := &teamWorld{gate: &accessGate{Store: st, reads: make(chan struct{}, 1)}}
	w.svc = board.New(w.gate, notify.NewInProcess(), clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	owner, err := w.svc.BootstrapOwner(ctx, "alex", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	w.alex = w.auth(ctx, t, owner)
	person := func(handle string) board.Principal {
		inv, err := w.svc.CreateServerInvite(ctx, w.alex, 0)
		if err != nil {
			t.Fatal(err)
		}
		c, err := w.svc.Connect(ctx, board.ConnectInput{Invite: inv.Secret, Handle: handle, KeyName: "laptop"})
		if err != nil {
			t.Fatal(err)
		}
		return w.auth(ctx, t, c.Token)
	}
	w.maya, w.sam = person("maya"), person("sam")
	v, err := w.svc.CreateBoard(ctx, w.maya, board.NewBoard{Template: "general", Visibility: board.BoardPrivate})
	if err != nil {
		t.Fatal(err)
	}
	w.board = v.Board.Name
	if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	w.samAgent = w.auth(ctx, t, joined.Token)
	return w
}

func (w *teamWorld) auth(ctx context.Context, t *testing.T, token string) board.Principal {
	t.Helper()
	p, err := w.svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// gatedRead runs read while it waits before its transaction, runs change in that wait,
// and returns read's error.
func (w *teamWorld) gatedRead(t *testing.T, read func() error, change func()) error {
	t.Helper()
	waiting, release := w.gate.arm()
	done := make(chan error, 1)
	go func() { done <- read() }()
	<-waiting
	change()
	close(release)
	return <-done
}

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if e, ok := apierr.As(err); !ok || e.Code != code {
		t.Fatalf("%s: got %v, want %s", what, err, code)
	}
}

// reads are the reads of a board's content a person on it can make.
var reads = map[string]func(ctx context.Context, w *teamWorld, p board.Principal) error{
	"timeline": func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.Timeline(ctx, p, w.board, board.TimelineFilter{Limit: 10})
		return err
	},
	"events": func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.Events(ctx, p, w.board, 0, 10)
		return err
	},
	"members": func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.Members(ctx, p, w.board)
		return err
	},
	"people": func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.People(ctx, p, w.board)
		return err
	},
	"board": func(ctx context.Context, w *teamWorld, p board.Principal) error {
		_, err := w.svc.GetBoard(ctx, p, w.board)
		return err
	},
}

// A person removed from a private board while their read waits for its transaction
// reads nothing: the read checks access inside its own transaction. Their agent loses the
// board with them.
func TestRemovalWhileAReadWaitsEndsTheRead(t *testing.T) {
	for name, read := range reads {
		for _, who := range []string{"person", "agent"} {
			t.Run(name+" by the "+who, func(t *testing.T) {
				w := newTeamWorld(t)
				ctx := context.Background()
				p := w.sam
				if who == "agent" {
					p = w.samAgent
				}
				if err := read(ctx, w, p); err != nil {
					t.Fatalf("before removal: %v", err)
				}
				err := w.gatedRead(t, func() error { return read(ctx, w, p) }, func() {
					if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
						t.Error(err)
					}
				})
				wantCode(t, name, err, "board_not_found")
			})
		}
	}
}

// An agent's inbox long poll, waiting for a message, ends with board_not_found when its
// person is removed, rather than waiting on or reading on.
func TestRemovalEndsAnAgentsWaitingInbox(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	done := make(chan error, 1)
	// Drain the reads made while setting up, so the next one is the poll's.
	select {
	case <-w.gate.reads:
	default:
	}
	go func() {
		_, _, err := w.svc.Inbox(ctx, w.samAgent, time.Hour, 0, 10)
		done <- err
	}()
	<-w.gate.reads // the poll read once and found nothing
	if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		wantCode(t, "the waiting inbox", err, "board_not_found")
	case <-time.After(5 * time.Second):
		t.Fatal("the inbox kept waiting after its person was removed")
	}
}

// A removed person's stream of heads stops reporting the board: a message posted after
// the removal never reaches it.
func TestRemovalStopsAPersonsStreamOfTheBoard(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	feed, err := w.svc.FollowHeads(w.sam)
	if err != nil {
		t.Fatal(err)
	}
	tick := make(chan time.Time)
	first, _, err := feed.Next(ctx, tick)
	if err != nil || len(first.Heads) != 1 || first.Heads[0].Board != w.board {
		t.Fatalf("first update: %+v %v", first, err)
	}
	if _, err := w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "after sam left"}); err != nil {
		t.Fatal(err)
	}
	go func() { tick <- time.Now() }()
	u, _, err := feed.Next(ctx, tick)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range u.Heads {
		if h.Board == w.board {
			t.Fatalf("the stream reported the board after removal: %+v", u)
		}
	}
	if len(u.Presence) != 0 {
		t.Fatalf("the stream reported presence on the board after removal: %+v", u)
	}
}

// Turning an open board private while a person who isn't on it waits to look at it
// leaves them seeing nothing: board_not_found, as for a board that doesn't exist.
func TestTurningPrivateWhileAnOutsidersReadWaits(t *testing.T) {
	for _, name := range []string{"people", "board"} {
		t.Run(name, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			if _, err := w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardOpen, false); err != nil {
				t.Fatal(err)
			}
			if err := reads[name](ctx, w, w.alex); err != nil {
				t.Fatalf("alex looking at the open board: %v", err)
			}
			err := w.gatedRead(t, func() error { return reads[name](ctx, w, w.alex) }, func() {
				if _, err := w.svc.SetVisibility(ctx, w.maya, w.board, board.BoardPrivate, false); err != nil {
					t.Error(err)
				}
			})
			wantCode(t, name, err, "board_not_found")
		})
	}
}
