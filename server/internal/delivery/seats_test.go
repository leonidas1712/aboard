package delivery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
)

// fakeSeats is a server reached through a delegation, and a credentials file, in memory.
type fakeSeats struct {
	mu       sync.Mutex
	boards   []delivery.SeatBoard
	archived *int
	// lifecycles are the lifecycle filters Boards was asked for, in order.
	lifecycles []string
	joinErr    error
	saveErr    error
	seats      map[string]delivery.SeatRef // by board
	saved      map[string]string           // token by member id
	joins      int
	inFlight   int
	most       int
	// beforeSave runs as Save starts, outside the lock.
	beforeSave func()
	// gate, when set, holds every Join until it is closed.
	gate chan struct{}
}

func newFakeSeats() *fakeSeats {
	return &fakeSeats{seats: map[string]delivery.SeatRef{}, saved: map[string]string{}}
}

func (f *fakeSeats) Boards(_ context.Context, _, lifecycle string) (delivery.SeatBoards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lifecycles = append(f.lifecycles, lifecycle)
	if f.joinErr != nil {
		return delivery.SeatBoards{}, f.joinErr
	}
	return delivery.SeatBoards{Boards: f.boards, ArchivedCount: f.archived}, nil
}

func (f *fakeSeats) Join(_ context.Context, server string, req delivery.SeatRequest) (delivery.SeatGrant, error) {
	f.mu.Lock()
	f.joins++
	f.inFlight++
	f.most = max(f.most, f.inFlight)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	if f.joinErr != nil {
		return delivery.SeatGrant{}, f.joinErr
	}
	seat, reused := f.seats[req.Board]
	if !reused {
		seat = delivery.SeatRef{Server: server, Board: req.Board, Name: "claude", MemberID: fmt.Sprintf("mem_%026d", len(f.seats)+1)}
		f.seats[req.Board] = seat
	}
	return delivery.SeatGrant{
		Seat: seat, Token: fmt.Sprintf("aba_token-%d", f.joins), Reused: reused,
		Board: json.RawMessage(`{"name":"` + req.Board + `"}`), Member: json.RawMessage(`{"id":"` + seat.MemberID + `"}`), Mode: delivery.ModeFocused,
	}, nil
}

func (f *fakeSeats) Save(_ context.Context, seat delivery.SeatRef, token string) error {
	if f.beforeSave != nil {
		f.beforeSave()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved[seat.MemberID] = token
	return nil
}

func (f *fakeSeats) SeatID(agent delivery.AgentRef) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.seats[agent.Board]
	return s.MemberID, ok
}

// seatsRig is a rig whose daemon joins boards through f.
func seatsRig(t *testing.T) (*rig, *fakeSeats) {
	t.Helper()
	return seatsRigWith(t, nil)
}

// seatsRigWith is seatsRig with configure applied to the daemon's config.
func seatsRigWith(t *testing.T, configure func(*delivery.Config)) (*rig, *fakeSeats) {
	t.Helper()
	f := newFakeSeats()
	r := &rig{
		t: t, clock: clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)),
		claude:  deliverytest.NewFakeAdapter("claude-code", true),
		codex:   deliverytest.NewFakeAdapter("codex", false),
		server:  deliverytest.NewFakeServer(),
		procs:   deliverytest.NewFakeProcesses(),
		path:    filepath.Join(t.TempDir(), "delivery.db"),
		tickets: launchtickets.Dir(t.TempDir()),
		seats:   f, configure: configure,
	}
	r.start()
	t.Cleanup(r.stop)
	return r, f
}

func (r *rig) join(id, board string) delivery.Response {
	r.t.Helper()
	return r.call(delivery.Request{Op: delivery.OpJoin, Harness: "claude-code", Session: id, Agent: &delivery.AgentRef{Server: serverURL, Board: board}})
}

// agentsOf returns the agents bound to the Claude Code session s1.
func (r *rig) agentsOf() []delivery.AgentRef {
	r.t.Helper()
	return r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"}).Agents
}

// A join saves the seat's token before it binds the seat, then binds it as bind does,
// and a second join for the same board gets the same seat back from the server.
func TestAJoinSavesTheTokenBeforeItBinds(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	var boundAtSave []delivery.AgentRef
	f.beforeSave = func() { boundAtSave = r.agentsOf() }

	resp := r.join("s1", "docs")
	if resp.Error != nil || resp.Joined == nil || resp.Joined.Board != "docs" || resp.Joined.MemberID == "" || resp.Reused ||
		resp.Mode != delivery.ModeFocused || resp.Previous != nil || string(resp.Board) != `{"name":"docs"}` {
		t.Fatalf("join: %+v", resp)
	}
	if len(boundAtSave) != 0 {
		t.Errorf("the seat was bound before its token was saved: %v", boundAtSave)
	}
	if got := r.agentsOf(); len(got) != 1 || got[0].Board != "docs" || got[0].Name != "claude" {
		t.Errorf("bound: %v", got)
	}
	if f.saved[resp.Joined.MemberID] != "aba_token-1" {
		t.Errorf("saved: %v", f.saved)
	}
	again := r.join("s1", "docs")
	if again.Error != nil || !again.Reused || again.Joined.MemberID != resp.Joined.MemberID || f.saved[resp.Joined.MemberID] != "aba_token-2" {
		t.Fatalf("second join: %+v, saved %v", again, f.saved)
	}
	if f.joins != 2 {
		t.Errorf("the daemon answered a join from its own records: %d server joins", f.joins)
	}
}

// Joining another board preserves the first seat and its immutable identity.
func TestAJoinRetainsEachBoardsSeat(t *testing.T) {
	r, _ := seatsRig(t)
	r.register("s1", "b1")
	first := r.join("s1", "docs")
	if first.Error != nil || first.Joined == nil {
		t.Fatalf("first board: %+v", first)
	}
	resp := r.join("s1", "plans")
	if resp.Error != nil || resp.Previous != nil || resp.Joined == nil {
		t.Fatalf("second board: %+v", resp)
	}
	got := r.agentsOf()
	if len(got) != 2 {
		t.Fatalf("bound: %v", got)
	}
	ids := map[string]string{}
	for _, a := range got {
		ids[a.Board] = a.MemberID
	}
	if ids["docs"] != first.Joined.MemberID || ids["plans"] != resp.Joined.MemberID || ids["docs"] == ids["plans"] {
		t.Fatalf("board identities: %v", ids)
	}
	if st := r.status(); !st.MultiSeat {
		t.Errorf("status omits multi_seat")
	}
}

// A join that fails binds nothing and keeps the session's binding as it was.
func TestAFailedJoinBindsNothing(t *testing.T) {
	removed := &delivery.WireError{Code: "agent_removed", Message: "removed", Hint: "ask", Details: map[string]any{"removed_by": "board_owner"}}
	for _, tc := range []struct {
		name          string
		joinErr       error
		saveErr       error
		code          string
		serverCalled  bool
		wantDetailsBy string
	}{
		{"server unreachable", fmt.Errorf("dial: %w", delivery.ErrServerUnreachable), nil, "server_unreachable", true, ""},
		{"lost answer", errors.New("read: connection reset"), nil, "server_unreachable", true, ""},
		{"server refusal", removed, nil, "agent_removed", true, "board_owner"},
		{"no key", delivery.ErrLoginMissing, nil, "login_required", true, ""},
		{"old server", delivery.ErrServerOutdated, nil, "server_outdated", true, ""},
		{"save fails", nil, errors.New("disk full"), "internal", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := seatsRig(t)
			r.register("s1", "b1")
			r.bind("claude-code", "s1", reviewer)
			f.joinErr, f.saveErr = tc.joinErr, tc.saveErr
			resp := r.join("s1", "plans")
			if resp.Error == nil || resp.Error.Code != tc.code {
				t.Fatalf("join: %+v", resp)
			}
			if tc.wantDetailsBy != "" && resp.Error.Details["removed_by"] != tc.wantDetailsBy {
				t.Errorf("details: %+v", resp.Error)
			}
			if got := r.agentsOf(); len(got) != 1 || got[0] != reviewer {
				t.Errorf("bound after a failed join: %v", got)
			}
		})
	}
}

// The daemon vouches only for a session it knows, on the server its seats are on.
func TestAJoinIsOnlyForAKnownSessionOnItsServer(t *testing.T) {
	r, f := seatsRig(t)
	if resp := r.join("unknown", "docs"); resp.Error == nil || resp.Error.Code != "session_unknown" {
		t.Fatalf("unknown session: %+v", resp)
	}
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	elsewhere := delivery.AgentRef{Server: "https://team.example.com", Board: "docs"}
	other := r.call(delivery.Request{Op: delivery.OpJoin, Harness: "claude-code", Session: "s1", Agent: &elsewhere})
	if other.Error == nil || other.Error.Code != "session_on_another_server" || other.Error.Details["session_server"] != serverURL {
		t.Fatalf("another server: %+v", other)
	}
	if f.joins != 0 {
		t.Errorf("the server was asked %d times", f.joins)
	}
}

// Two first joins from one session to two servers, at once: exactly one gets a seat,
// the other gets session_on_another_server, and only one seat is bound. Each join is
// held where it has read the session's seats and not yet checked its server; both are
// released together once both are there, or once the second is seen waiting for the
// session's turn instead.
func TestFirstJoinsToTwoServersAtOnceGiveOneSeat(t *testing.T) {
	arrived, waited, release := make(chan struct{}, 2), make(chan struct{}, 2), make(chan struct{})
	r, f := seatsRigWith(t, func(cfg *delivery.Config) {
		delivery.WithJoinHooks(cfg, delivery.JoinHooks{
			SeatsRead: func() { arrived <- struct{}{}; <-release },
			Waiting:   func() { waited <- struct{}{} },
		})
	})
	r.register("s1", "b1")
	servers := []string{serverURL, "https://team.example.com"}
	answers := make([]delivery.Response, 2)
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent := delivery.AgentRef{Server: srv, Board: fmt.Sprintf("board-%d", i)}
			answers[i] = r.call(delivery.Request{Op: delivery.OpJoin, Harness: "claude-code", Session: "s1", Agent: &agent})
		}()
	}
	deadline := time.After(within)
	select {
	case <-arrived:
	case <-deadline:
		t.Fatal("no join read the session's seats")
	}
	select {
	case <-arrived:
	case <-waited:
	case <-deadline:
		t.Fatal("the second join neither read the seats nor waited for the session's turn")
	}
	close(release)
	wg.Wait()
	ok, other := 0, 0
	for _, a := range answers {
		switch {
		case a.Error == nil:
			ok++
		case a.Error.Code == "session_on_another_server":
			other++
		default:
			t.Fatalf("join: %+v", a.Error)
		}
	}
	if ok != 1 || other != 1 || f.joins != 1 || len(f.saved) != 1 {
		t.Fatalf("%d joined, %d refused, %d server joins, saved %v", ok, other, f.joins, f.saved)
	}
	if got := r.agentsOf(); len(got) != 1 {
		t.Fatalf("bound: %v", got)
	}
}

// Joins for one session and board run one at a time.
func TestJoinsForOneSeatTakeTurns(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	f.gate = make(chan struct{})
	var wg sync.WaitGroup
	answers := make([]delivery.Response, 3)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = r.join("s1", "docs")
		}()
	}
	deadline := time.After(within)
	for {
		f.mu.Lock()
		started := f.joins
		f.mu.Unlock()
		if started >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no join reached the server")
		case <-time.After(5 * time.Millisecond): // a poll interval
		}
	}
	close(f.gate)
	wg.Wait()
	if f.most != 1 || f.joins != 3 {
		t.Fatalf("%d joins, at most %d at once", f.joins, f.most)
	}
	for _, a := range answers {
		if a.Error != nil {
			t.Fatalf("join: %+v", a.Error)
		}
	}
}

// boards lists what the server gives, marking the session's seat.
func TestBoardsMarksTheSessionsSeat(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	f.boards = []delivery.SeatBoard{
		{Name: "docs", Board: json.RawMessage(`{"name":"docs","visibility":"open"}`)},
		{Name: "plans", Board: json.RawMessage(`{"name":"plans","visibility":"private"}`)},
	}
	r.join("s1", "docs")
	resp := r.ok(delivery.Request{Op: delivery.OpBoards, Harness: "claude-code", Session: "s1", Server: serverURL})
	if resp.Server != serverURL || len(resp.Boards) != 2 {
		t.Fatalf("boards: %+v", resp)
	}
	var docs, plans map[string]any
	_ = json.Unmarshal(resp.Boards[0], &docs)
	_ = json.Unmarshal(resp.Boards[1], &plans)
	seat, _ := docs["seat"].(map[string]any)
	if seat["name"] != "claude" || seat["member_id"] == "" || plans["seat"] != nil {
		t.Errorf("docs %v, plans %v", docs, plans)
	}
	if resp := r.call(delivery.Request{Op: delivery.OpBoards, Harness: "claude-code", Session: "nope", Server: serverURL}); resp.Error == nil || resp.Error.Code != "session_unknown" {
		t.Errorf("unknown session: %+v", resp)
	}
}

// boards passes the lifecycle filter to the server and the server's archived count
// back, and refuses a filter the API doesn't have before asking the server.
func TestBoardsPassesTheLifecycleFilterAndTheArchivedCount(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	two := 2
	f.archived = &two
	f.boards = []delivery.SeatBoard{{Name: "old", Board: json.RawMessage(`{"name":"old","visibility":"open","lifecycle":"archived"}`)}}
	resp := r.ok(delivery.Request{Op: delivery.OpBoards, Harness: "claude-code", Session: "s1", Server: serverURL, Lifecycle: "archived"})
	if resp.ArchivedCount == nil || *resp.ArchivedCount != 2 || len(resp.Boards) != 1 {
		t.Fatalf("archived boards: %+v", resp)
	}
	f.archived = nil
	if resp := r.ok(delivery.Request{Op: delivery.OpBoards, Harness: "claude-code", Session: "s1", Server: serverURL}); resp.ArchivedCount != nil {
		t.Errorf("a count the server didn't send: %d", *resp.ArchivedCount)
	}
	if resp := r.call(delivery.Request{Op: delivery.OpBoards, Harness: "claude-code", Session: "s1", Server: serverURL, Lifecycle: "deleted"}); resp.Error == nil || resp.Error.Code != "invalid_request" {
		t.Errorf("a filter the API doesn't have: %+v", resp)
	}
	if got := fmt.Sprint(f.lifecycles); got != "[archived ]" {
		t.Errorf("filters sent to the server: %s", got)
	}
}

func (f *fakeSeats) Create(ctx context.Context, server string, req delivery.SeatCreateRequest) (delivery.SeatGrant, error) {
	return f.Join(ctx, server, delivery.SeatRequest{Board: req.BoardCreateOptions.Name, Harness: req.Harness, Session: req.Session, Name: req.Name, Role: req.Role})
}

func TestCreationSavesBeforeBindingAndRetainsSiblingSeats(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	if first := r.join("s1", "docs"); first.Error != nil {
		t.Fatal(first.Error)
	}
	f.beforeSave = func() {
		if got := r.agentsOf(); len(got) != 1 || got[0].Board != "docs" {
			t.Errorf("creation bound before save: %v", got)
		}
	}
	response := r.call(delivery.Request{Op: delivery.OpCreateBoard, Harness: "claude-code", Session: "s1", Server: serverURL, Create: &delivery.BoardCreateOptions{Name: "new-work"}, IdempotencyKey: "operation-1"})
	if response.Error != nil || response.Joined == nil {
		t.Fatalf("create: %+v", response)
	}
	if got := r.agentsOf(); len(got) != 2 {
		t.Fatalf("siblings were lost: %v", got)
	}
}

func TestCreationRefusesUnsupportedExtensionBeforeServerOrCredentialWrites(t *testing.T) {
	r, f := seatsRig(t)
	_, welcome := r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b1"})
	if welcome.Error != nil {
		t.Fatal(welcome.Error)
	}
	if first := r.call(delivery.Request{Op: delivery.OpJoin, Harness: "omp", Session: "o1", Agent: &delivery.AgentRef{Server: serverURL, Board: "docs"}}); first.Error != nil {
		t.Fatal(first.Error)
	}
	response := r.call(delivery.Request{Op: delivery.OpCreateBoard, Harness: "omp", Session: "o1", Server: serverURL, IdempotencyKey: "operation-1"})
	if response.Error == nil || response.Error.Code != "extension_outdated" {
		t.Fatalf("create: %+v", response)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.joins != 1 || len(f.saved) != 1 {
		t.Fatalf("refusal wrote state: joins=%d saved=%d", f.joins, len(f.saved))
	}
}

func TestCreationSaveFailureBindsNothing(t *testing.T) {
	r, f := seatsRig(t)
	r.register("s1", "b1")
	f.saveErr = errors.New("read-only credentials")
	response := r.call(delivery.Request{Op: delivery.OpCreateBoard, Harness: "claude-code", Session: "s1", Server: serverURL, Create: &delivery.BoardCreateOptions{Name: "new-work"}, IdempotencyKey: "operation-1"})
	if response.Error == nil || response.Error.Code != "internal" || len(r.agentsOf()) != 0 {
		t.Fatalf("save failure bound: %+v", response)
	}
}
