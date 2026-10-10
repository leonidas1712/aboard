package delivery_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type readyPairingRuntime struct {
	mu     sync.Mutex
	row    delivery.PairingRequest
	writes int
}

func (p *readyPairingRuntime) List(context.Context) ([]delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return []delivery.PairingRequest{p.row}, nil
}

func (p *readyPairingRuntime) Get(context.Context, string) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.row, nil
}

func (p *readyPairingRuntime) PersonID(context.Context) (string, error) { return "hum_recipient", nil }

func (p *readyPairingRuntime) Create(context.Context, delivery.PairingCreate, delivery.AgentRef, string) (delivery.PairingRequest, error) {
	panic("unexpected create")
}

func (p *readyPairingRuntime) Close(context.Context, string, string, string) (delivery.PairingRequest, error) {
	panic("unexpected close")
}

func (p *readyPairingRuntime) Select(_ context.Context, _ delivery.PairingRequest, _ string, a delivery.AgentRef, binding string, _ bool, _ string) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	p.row.State = "verifying"
	p.row.Recipient = &delivery.PairingEndpoint{PersonID: "hum_recipient", AgentID: a.MemberID, Generation: 1, SessionBinding: binding}
	return p.row, nil
}

func (p *readyPairingRuntime) Progress(context.Context, delivery.PairingRequest, delivery.AgentRef, string, []delivery.Delivery) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.row, nil
}

func TestReadyPairingRerunRequiresExactCurrentEndpoint(t *testing.T) {
	p := &readyPairingRuntime{row: delivery.PairingRequest{ID: "prq_exact", ServerID: "srv_one", BoardID: "brd_docs", InviterID: "hum_inviter", RecipientID: "hum_recipient", State: "pending", Generation: 1}}
	var removed atomic.Bool
	r, f := seatsRigWith(t, func(c *delivery.Config) {
		c.PairingFor = func(string) delivery.PairingRuntime { return p }
		c.ResolveAgent = func(_ context.Context, a delivery.AgentRef) (delivery.AgentRef, error) {
			if removed.Load() {
				return delivery.AgentRef{}, &delivery.WireError{Code: "agent_removed", Message: "The seat was removed.", Hint: "Ask your person."}
			}
			return a, nil
		}
	})
	f.boards = []delivery.SeatBoard{{Name: "docs", Board: json.RawMessage(`{"id":"brd_docs"}`)}}
	r.register("s1", "boot1")
	req := delivery.Request{Op: delivery.OpPairing, Harness: "claude-code", Session: "s1", Server: serverURL, PairingAction: "accept", PairingID: "prq_exact", IdempotencyKey: "accept-exact"}
	first := r.call(req)
	if first.Error != nil || first.Pairing == nil {
		t.Fatalf("initial acceptance: %+v", first)
	}
	p.mu.Lock()
	p.row.State = "ready"
	original := *p.row.Recipient
	p.mu.Unlock()
	ready := r.call(req)
	if ready.Error != nil || ready.Pairing == nil || ready.Pairing.State != "ready" {
		t.Fatalf("ready rerun: %+v", ready)
	}
	p.mu.Lock()
	writes := p.writes
	p.mu.Unlock()
	if writes != 1 {
		t.Fatalf("ready read reselected endpoint: %d writes", writes)
	}

	t.Run("replace", func(t *testing.T) {
		q := req
		q.Replace = true
		got := r.call(q)
		if got.Error == nil || got.Error.Code != "pairing_closed" {
			t.Fatalf("replace: %+v", got)
		}
	})
	t.Run("other session", func(t *testing.T) {
		r.register("s2", "boot2")
		q := req
		q.Session = "s2"
		got := r.call(q)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("other session: %+v", got)
		}
	})
	t.Run("endpoint generation", func(t *testing.T) {
		p.mu.Lock()
		e := original
		e.Generation = 2
		p.row.Recipient = &e
		p.mu.Unlock()
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("generation: %+v", got)
		}
		p.mu.Lock()
		p.row.Recipient = &original
		p.mu.Unlock()
	})
	t.Run("stale binding", func(t *testing.T) {
		p.mu.Lock()
		e := original
		e.SessionBinding = "sha256:old"
		p.row.Recipient = &e
		p.mu.Unlock()
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("binding: %+v", got)
		}
		p.mu.Lock()
		p.row.Recipient = &original
		p.mu.Unlock()
	})
	t.Run("membership lost", func(t *testing.T) {
		f.mu.Lock()
		old := f.boards
		f.boards = nil
		f.mu.Unlock()
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "board_not_found" {
			t.Fatalf("access: %+v", got)
		}
		f.mu.Lock()
		f.boards = old
		f.mu.Unlock()
	})
	t.Run("removed seat", func(t *testing.T) {
		removed.Store(true)
		got := r.call(req)
		removed.Store(false)
		if got.Error == nil || got.Error.Code != "agent_removed" {
			t.Fatalf("removed: %+v", got)
		}
	})
	t.Run("session boot", func(t *testing.T) {
		sessions, err := r.journal.Sessions(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var original delivery.SessionRecord
		for _, s := range sessions {
			if s.Key.ID == "s1" {
				original = s
			}
		}
		changed := original
		changed.Boot = "replacement-boot"
		if err := r.journal.SaveSession(t.Context(), changed); err != nil {
			t.Fatal(err)
		}
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("boot: %+v", got)
		}
		if err := r.journal.SaveSession(t.Context(), original); err != nil {
			t.Fatal(err)
		}
		got = r.call(req)
		if got.Error != nil {
			t.Fatalf("original boot: %+v", got)
		}
	})
	t.Run("local binding generation", func(t *testing.T) {
		bindings, err := r.journal.Bindings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 1 {
			t.Fatalf("bindings: %+v", bindings)
		}
		if _, err := r.journal.BindGeneration(t.Context(), bindings[0], true); err != nil {
			t.Fatal(err)
		}
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("local generation: %+v", got)
		}
	})
	t.Run("daemon restart", func(t *testing.T) {
		r.stop()
		r.start()
		got := r.call(req)
		if got.Error == nil || got.Error.Code != "pairing_changed" {
			t.Fatalf("restart: %+v", got)
		}
	})
}

func TestPairingIgnoresUnrelatedIssuerSeats(t *testing.T) {
	p := &readyPairingRuntime{row: delivery.PairingRequest{ID: "prq_other", ServerID: "srv_one", BoardID: "brd_docs", InviterID: "hum_inviter", RecipientID: "hum_recipient", State: "pending", Generation: 1}}
	r, f := seatsRigWith(t, func(c *delivery.Config) { c.PairingFor = func(string) delivery.PairingRuntime { return p } })
	f.boards = []delivery.SeatBoard{{Name: "docs", Board: json.RawMessage(`{"id":"brd_docs"}`)}}
	r.register("s1", "boot1")
	joined := r.call(delivery.Request{Op: delivery.OpJoin, Harness: "claude-code", Session: "s1", Agent: &delivery.AgentRef{Server: "https://other.example", Board: "other", Name: "other"}})
	if joined.Error != nil {
		t.Fatalf("unrelated join: %+v", joined)
	}
	got := r.call(delivery.Request{Op: delivery.OpPairing, Harness: "claude-code", Session: "s1", Server: serverURL, PairingAction: "accept", PairingID: "prq_other", IdempotencyKey: "accept-other"})
	if got.Error != nil || got.Pairing == nil {
		t.Fatalf("requested issuer blocked by unrelated seat: %+v", got)
	}
}
