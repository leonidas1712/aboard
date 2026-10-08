package board_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

type headReads struct {
	board.Store
	mu           sync.Mutex
	all          int
	boards       []string
	fullMembers  int
	unreadCounts int
	fullBoards   int
}

type watchedHeads struct {
	board.Notifier
	keys map[string]bool
}

func (n *watchedHeads) Watch(key string) <-chan struct{} {
	n.keys[key] = true
	return n.Notifier.Watch(key)
}

func TestReceiptChangesWakeOnlyTheOwnersStream(t *testing.T) {
	w := newTeamWorld(t)
	n := &watchedHeads{Notifier: notify.NewInProcess(), keys: map[string]bool{}}
	w.svc = board.New(w.gate.Store, n, w.clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	watchReceipt := func(p board.Principal) <-chan struct{} {
		t.Helper()
		n.keys = map[string]bool{}
		feed, err := w.svc.FollowHeads(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := feed.Next(ctx, nil); err != nil {
			t.Fatal(err)
		}
		tick := make(chan time.Time, 1)
		tick <- time.Now()
		if _, _, err := feed.Next(ctx, tick); err != nil {
			t.Fatal(err)
		}
		for key := range n.keys {
			if strings.HasPrefix(key, "read/") {
				return n.Notifier.Watch(key)
			}
		}
		t.Fatal("stream did not watch its receipts")
		return nil
	}
	mine := watchReceipt(w.sam)
	other := watchReceipt(w.maya)
	seq := w.toSam(ctx, t)
	if _, err := w.svc.Ack(ctx, w.samAgent, seq); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mine:
	default:
		t.Error("receipt did not wake the agent's owner")
	}
	select {
	case <-other:
		t.Error("receipt woke another person's stream")
	default:
	}
}

func BenchmarkPostToManyBoardListeners(b *testing.B) {
	benchmarkHeadChanges(b, false)
}

func BenchmarkPresenceToManyBoardListeners(b *testing.B) {
	benchmarkHeadChanges(b, true)
}

func benchmarkHeadChanges(b *testing.B, presence bool) {
	b.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
	st, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "aboard.db"), clk)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	reads := &headReads{Store: st}
	svc := board.New(reads, notify.NewInProcess(), clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	token, err := svc.BootstrapOwner(ctx, "alex", "laptop")
	if err != nil {
		b.Fatal(err)
	}
	p, err := svc.Authenticate(ctx, token)
	if err != nil {
		b.Fatal(err)
	}
	var hot string
	for range 10 {
		v, err := svc.CreateBoard(ctx, p, board.NewBoard{Template: "general"})
		if err != nil {
			b.Fatal(err)
		}
		hot = v.Board.Name
	}
	var agent board.Principal
	if presence {
		joined, err := svc.Join(ctx, p, board.JoinInput{Board: hot, Role: "member"})
		if err != nil {
			b.Fatal(err)
		}
		agent, err = svc.Authenticate(ctx, joined.Token)
		if err != nil {
			b.Fatal(err)
		}
	}
	feeds := make([]*board.HeadFeed, 10)
	for i := range feeds {
		feeds[i], err = svc.FollowHeads(p)
		if err != nil {
			b.Fatal(err)
		}
		if _, _, err := feeds[i].Next(ctx, nil); err != nil {
			b.Fatal(err)
		}
		tick := make(chan time.Time, 1)
		tick <- time.Now()
		if _, _, err := feeds[i].Next(ctx, tick); err != nil {
			b.Fatal(err)
		}
	}
	reads.mu.Lock()
	reads.all, reads.boards = 0, nil
	reads.unreadCounts = 0
	reads.mu.Unlock()
	b.ResetTimer()
	for i := range b.N {
		if presence {
			state := board.PresenceWorking
			if i%2 != 0 {
				state = board.PresenceIdle
			}
			if _, _, err := svc.SetPresence(ctx, agent, state, "all"); err != nil {
				b.Fatal(err)
			}
		} else {
			if _, err := svc.PostMessage(ctx, p, hot, board.NewMessage{Body: "one hot board"}); err != nil {
				b.Fatal(err)
			}
		}
		for _, feed := range feeds {
			u, _, err := feed.Next(ctx, nil)
			if err != nil || (!presence && len(u.Heads) != 1) || (presence && len(u.Presence) != 1) {
				b.Fatalf("stream update: %+v %v", u, err)
			}
		}
	}
	b.StopTimer()
	reads.mu.Lock()
	b.ReportMetric(float64(reads.all)/float64(b.N), "membership-scans/post")
	b.ReportMetric(float64(len(reads.boards))/float64(b.N), "board-reads/post")
	b.ReportMetric(float64(reads.unreadCounts)/float64(b.N), "unread-counts/change")
	reads.mu.Unlock()
}

type headReadTx struct {
	board.ReadTx
	reads *headReads
}

func (s *headReads) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	return s.Store.Read(ctx, func(tx board.ReadTx) error { return fn(headReadTx{tx, s}) })
}

func (tx headReadTx) BoardsOfHuman(id string) ([]board.Board, error) {
	tx.reads.mu.Lock()
	tx.reads.all++
	tx.reads.mu.Unlock()
	return tx.ReadTx.BoardsOfHuman(id)
}

func (tx headReadTx) BoardByID(id string) (board.Board, error) {
	tx.reads.mu.Lock()
	tx.reads.boards = append(tx.reads.boards, id)
	tx.reads.fullBoards++
	tx.reads.mu.Unlock()
	return tx.ReadTx.BoardByID(id)
}

func (tx headReadTx) StreamBoard(id string) (board.Board, error) {
	tx.reads.mu.Lock()
	tx.reads.boards = append(tx.reads.boards, id)
	tx.reads.mu.Unlock()
	if narrow, ok := tx.ReadTx.(interface {
		StreamBoard(string) (board.Board, error)
	}); ok {
		return narrow.StreamBoard(id)
	}
	return tx.ReadTx.BoardByID(id)
}

func (tx headReadTx) Members(id string) ([]board.Member, error) {
	tx.reads.mu.Lock()
	tx.reads.fullMembers++
	tx.reads.mu.Unlock()
	return tx.ReadTx.Members(id)
}

func (tx headReadTx) CountUnread(m board.Member, addressedOnly, mentions bool) (int64, error) {
	tx.reads.mu.Lock()
	tx.reads.unreadCounts++
	tx.reads.mu.Unlock()
	return tx.ReadTx.CountUnread(m, addressedOnly, mentions)
}

func TestAStreamRefreshesOnlyTheBoardThatChanged(t *testing.T) {
	w := newKeyWorld(t)
	st := &headReads{Store: w.gate.Store}
	w.svc = board.New(st, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey,
		board.Config{ServerID: "srv_TEST", Mode: "local", JoinHost: "localhost"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := w.auth(t, w.owner)
	sibling, err := w.svc.CreateBoard(ctx, p, board.NewBoard{Template: "general"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := w.svc.FollowHeads(p)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := f.Next(ctx, nil)
	if err != nil || len(first.Heads) != 2 {
		t.Fatalf("initial heads: %+v %v", first, err)
	}
	// Reconcile the subscription's initial snapshot before measuring a later change.
	tick := make(chan time.Time, 1)
	tick <- time.Now()
	if _, _, err := f.Next(ctx, tick); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.all, st.boards, st.fullMembers, st.fullBoards = 0, nil, 0, 0
	st.mu.Unlock()
	if _, err := w.svc.PostMessage(ctx, p, w.board, board.NewMessage{Body: "one board changed"}); err != nil {
		t.Fatal(err)
	}
	u, _, err := f.Next(ctx, nil)
	if err != nil || len(u.Heads) != 1 || u.Heads[0].Board != w.board {
		t.Fatalf("changed head: %+v %v", u, err)
	}
	st.mu.Lock()
	if st.all != 0 {
		t.Errorf("board notification refreshed the complete membership list %d times", st.all)
	}
	if st.fullMembers != 0 {
		t.Errorf("stream fetched %d full member projections", st.fullMembers)
	}
	if st.fullBoards != 0 {
		t.Errorf("stream fetched %d full board projections", st.fullBoards)
	}
	for _, id := range st.boards {
		if id == sibling.Board.ID {
			t.Error("board notification refreshed the unchanged sibling")
		}
	}
	st.mu.Unlock()
	if _, _, err := w.svc.SetPresence(ctx, w.auth(t, w.agent), board.PresenceWorking, "all"); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.unreadCounts = 0
	st.mu.Unlock()
	if u, _, err := f.Next(ctx, nil); err != nil || len(u.Presence) != 1 {
		t.Fatalf("presence update: %+v %v", u, err)
	}
	st.mu.Lock()
	if st.unreadCounts != 0 {
		t.Errorf("presence-only change recounted messages %d times", st.unreadCounts)
	}
	st.mu.Unlock()
}
