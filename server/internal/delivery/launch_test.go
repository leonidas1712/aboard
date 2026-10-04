package delivery_test

import (
	"crypto/rand"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// A session started with a launch ticket is bound to the ticket's agent as it
// registers, and the ticket works once: a second session handing it in (one started
// from inside the first, which inherits its environment) gets no agent.
func TestLaunchTicketBindsTheSessionOnce(t *testing.T) {
	r := newRig(t)
	ticket, err := r.tickets.Write(rand.Reader, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	first := r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s-1", Boot: "b1", Launch: ticket})
	if len(first.Agents) != 1 || first.Agents[0] != reviewer {
		t.Fatalf("the launched session registered with agents %+v, want %v", first.Agents, reviewer)
	}
	if r.tickets.Exists(ticket) {
		t.Fatal("the ticket is still there after the session took it")
	}
	second := r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s-2", Boot: "b2", Launch: ticket})
	if len(second.Agents) != 0 {
		t.Fatalf("a second session took the same ticket: %+v", second.Agents)
	}
	st := r.status()
	if len(st.Bindings) != 1 || st.Bindings[0].Session != "claude-code:s-1" || !st.Bindings[0].Open {
		t.Fatalf("bindings = %+v, want reviewer on the open claude-code:s-1", st.Bindings)
	}
}

// A session counts as turned once it has run a turn, which status reports and the
// journal keeps across a restart: only such a session can be resumed by its harness.
func TestStatusSaysWhetherASessionHasRunATurn(t *testing.T) {
	r := newRig(t)
	r.register("s-1", "b1")
	r.bind("claude-code", "s-1", reviewer)
	if b := r.status().Bindings[0]; b.Turned {
		t.Fatalf("a session that ran no turn is turned: %+v", b)
	}
	r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s-1"})
	if b := r.status().Bindings[0]; !b.Turned {
		t.Fatalf("after a turn: %+v", b)
	}
	r.restart()
	if b := r.status().Bindings[0]; !b.Turned {
		t.Fatalf("after a restart: %+v", b)
	}
}

// A session that ended and runs a turn again (a harness that resumes it without its
// session-start hook, as Codex does) is open again, with the agent it still holds.
func TestATurnOpensAnEndedSessionAgain(t *testing.T) {
	r := newRig(t)
	r.register("s-1", "b1")
	r.bind("claude-code", "s-1", reviewer)
	r.ok(delivery.Request{Op: delivery.OpEnd, Harness: "claude-code", Session: "s-1"})
	if b := r.status().Bindings[0]; b.Open {
		t.Fatalf("after end: %+v", b)
	}
	r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s-1"})
	if b := r.status().Bindings[0]; !b.Open || b.Agent != reviewer {
		t.Fatalf("after a turn: %+v", b)
	}
}

// An extension's hello hands in the ticket the same way.
func TestLaunchTicketBindsAnExtensionSession(t *testing.T) {
	r := newRig(t)
	ticket, err := r.tickets.Write(rand.Reader, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	c := r.dial()
	defer func() { _ = c.Close() }()
	go func() {
		_ = delivery.WriteFrame(c, delivery.Request{
			V: delivery.ProtocolVersion, Op: delivery.OpHello, Harness: "omp", Session: "o-1", Boot: "b1",
			Process: &delivery.Process{PID: 4321, Start: 1}, Launch: ticket,
		})
	}()
	r.eventually("the omp session to take its seat", passStep, func() bool {
		for _, b := range r.status().Bindings {
			if b.Agent == reviewer && b.Session == "omp:o-1" && b.Open {
				return true
			}
		}
		return false
	})
}
