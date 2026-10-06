package delivery_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSecondSeatNeedsALiveCapableExtensionBeforeJoining(t *testing.T) {
	for _, capable := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "disconnected"}[capable], func(t *testing.T) {
			r, f := seatsRig(t)
			hello := delivery.Request{Harness: "omp", Session: "o1", Boot: "b1"}
			if capable {
				hello.Capabilities = []string{delivery.CapabilityHandoffV1}
			}
			e, welcome := r.connect(hello)
			if welcome.Error != nil {
				t.Fatal(welcome.Error)
			}
			join := func(board string) delivery.Response {
				return r.call(delivery.Request{Op: delivery.OpJoin, Harness: "omp", Session: "o1", Agent: &delivery.AgentRef{Server: serverURL, Board: board}})
			}
			if first := join("docs"); first.Error != nil {
				t.Fatal(first.Error)
			}
			if capable {
				_ = e.conn.Close()
				r.eventually("extension disconnect", 0, func() bool { return r.status().OpenSessions == 0 })
			}
			second := join("plans")
			if second.Error == nil || second.Error.Code != "extension_outdated" {
				t.Fatalf("second join: %+v", second)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.joins != 1 || len(f.saved) != 1 {
				t.Fatalf("refusal changed server or credentials: joins=%d saved=%d", f.joins, len(f.saved))
			}
		})
	}
}

type joinEntrySeats struct {
	*fakeSeats
	entered chan struct{}
	release chan struct{}
}

func (f *joinEntrySeats) Join(ctx context.Context, server string, req delivery.SeatRequest) (delivery.SeatGrant, error) {
	if req.Board == "plans" {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return delivery.SeatGrant{}, ctx.Err()
		}
	}
	return f.fakeSeats.Join(ctx, server, req)
}

func TestExtensionLostDuringJoinDoesNotSaveOrBindTheGrant(t *testing.T) {
	r, f := seatsRig(t)
	r.stop()
	gated := &joinEntrySeats{fakeSeats: f, entered: make(chan struct{}), release: make(chan struct{})}
	r.seats = gated
	r.start()
	e, welcome := r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b1", Capabilities: []string{delivery.CapabilityHandoffV1}})
	if welcome.Error != nil {
		t.Fatal(welcome.Error)
	}
	join := func(board string) delivery.Response {
		return r.call(delivery.Request{Op: delivery.OpJoin, Harness: "omp", Session: "o1", Agent: &delivery.AgentRef{Server: serverURL, Board: board}})
	}
	if first := join("docs"); first.Error != nil {
		t.Fatal(first.Error)
	}
	result := make(chan delivery.Response, 1)
	go func() { result <- join("plans") }()
	await(t, r.clock, gated.entered, "second join entered the server")
	_ = e.conn.Close()
	r.eventually("extension disconnect", 0, func() bool { return r.status().OpenSessions == 0 })
	close(gated.release)
	second, _ := await(t, r.clock, result, "second join refusal")
	if second.Error == nil || second.Error.Code != "extension_outdated" {
		t.Fatalf("second join: %+v", second)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saved) != 1 {
		t.Fatalf("saved a grant after capability was lost: %v", f.saved)
	}
	agents := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "omp", Session: "o1"}).Agents
	if len(agents) != 1 || agents[0].Board != "docs" {
		t.Fatalf("bindings changed after failed join: %+v", agents)
	}
}
