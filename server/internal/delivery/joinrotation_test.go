package delivery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// rotationServer captures the credential used when an HTTP inbox request starts. A
// request held across a join returns its old-token refusal, even after Save replaces it.
type rotationServer struct {
	delivery.Server
	seats    *fakeSeats
	mu       sync.Mutex
	armed    bool
	entered  chan struct{}
	release  chan struct{}
	newReads int
	terminal bool
}

func (s *rotationServer) Inbox(ctx context.Context, a delivery.AgentRef) ([]delivery.Message, int, *delivery.HeldMode, error) {
	s.seats.mu.Lock()
	token := s.seats.saved[a.MemberID]
	s.seats.mu.Unlock()
	s.mu.Lock()
	gated := s.armed
	s.armed = false
	terminal := s.terminal
	if token == "aba_token-2" {
		s.newReads++
	}
	s.mu.Unlock()
	if gated {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, 0, nil, ctx.Err()
		}
		if terminal {
			return nil, 0, nil, delivery.ErrBoardGone
		}
		return nil, 0, nil, delivery.ErrUnauthorized
	}
	return s.Server.Inbox(ctx, a)
}

func TestRepeatedJoinRefreshesAfterAnOldTokenRefusal(t *testing.T) {
	r, f := seatsRig(t)
	r.status()
	r.stop()
	srv := &rotationServer{Server: r.server, seats: f, entered: make(chan struct{}), release: make(chan struct{})}
	r.remote = srv
	r.start()
	r.register("s1", "b1")
	joined := r.join("s1", "docs")
	if joined.Error != nil {
		t.Fatal(joined.Error)
	}
	a := r.agentsOf()[0]
	r.eventually("first inbox", 0, func() bool { return r.server.Requests(a) > 0 })
	before := r.server.Requests(a)
	h := r.wait("s1", "b1", false)
	r.eventually("hook inbox before rotation", 0, func() bool { return r.server.Requests(a) > before })
	srv.mu.Lock()
	srv.armed = true
	srv.mu.Unlock()
	r.post(a, "pending across token rotation", true)
	select {
	case <-srv.entered:
	case <-time.After(within):
		t.Fatal("old inbox did not start")
	}
	again := r.join("s1", "docs")
	if again.Error != nil || !again.Reused {
		t.Fatalf("repeat join: %+v", again)
	}
	close(srv.release)
	r.eventually("fresh credential inbox after repeated join", 0, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.newReads > 0 })
	r.eventually("pending delivery after rotation", 10*time.Millisecond, func() bool { return len(r.claude.Handed("s1")) > 0 })
	if got := h.bundle(); got == "" {
		t.Fatal("empty delivery")
	}
}

func TestRepeatedJoinNeverClearsTerminalBoardGone(t *testing.T) {
	r, f := seatsRig(t)
	r.status()
	r.stop()
	srv := &rotationServer{Server: r.server, seats: f, entered: make(chan struct{}), release: make(chan struct{}), terminal: true}
	r.remote = srv
	r.start()
	r.register("s1", "b1")
	joined := r.join("s1", "docs")
	if joined.Error != nil {
		t.Fatal(joined.Error)
	}
	a := r.agentsOf()[0]
	r.eventually("first inbox", 0, func() bool { return r.server.Requests(a) > 0 })
	before := r.server.Requests(a)
	h := r.wait("s1", "b1", false)
	r.eventually("hook inbox before rotation", 0, func() bool { return r.server.Requests(a) > before })
	srv.mu.Lock()
	srv.armed = true
	srv.mu.Unlock()
	r.post(a, "pending across token rotation", true)
	select {
	case <-srv.entered:
	case <-time.After(within):
		t.Fatal("old inbox did not start")
	}
	again := r.join("s1", "docs")
	if again.Error != nil || !again.Reused {
		t.Fatalf("repeat join: %+v", again)
	}
	close(srv.release)
	r.eventually("terminal refusal", 0, func() bool {
		st := r.status()
		return len(st.Agents) == 1 && st.Agents[0].Reason == delivery.ReasonBoardGone
	})
	again = r.join("s1", "docs")
	if again.Error != nil {
		t.Fatalf("fixture join: %+v", again)
	}
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "claude-code", Session: "s1"})
	st := r.status()
	if len(st.Agents) != 1 || st.Agents[0].Reason != delivery.ReasonBoardGone {
		t.Fatalf("terminal seat revived: %+v", st.Agents)
	}
	srv.mu.Lock()
	reads := srv.newReads
	srv.mu.Unlock()
	if reads != 0 || len(r.claude.Handed("s1")) != 0 {
		t.Fatalf("terminal seat delivered or refreshed: reads=%d", reads)
	}
	_ = h
}
