package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// broadcast posts another agent's message to everyone, which doesn't concern the agent.
func (r *rig) broadcast(to delivery.AgentRef, body string) int {
	return r.server.Post(to, delivery.Message{Body: body, To: []string{"all"}})
}

// nothingFor checks the hook is given nothing while the fake clock moves past the time
// the daemon gathers messages.
func (h *hook) nothingFor(why string) {
	h.t.Helper()
	for moved := time.Duration(0); moved < passLimit; moved += passStep {
		select {
		case resp := <-h.events:
			h.t.Fatalf("%s: the hook was given %+v", why, resp)
		case <-time.After(5 * time.Millisecond): // a poll interval, not a wait for the daemon
			h.clock.Advance(passStep)
		}
	}
}

// In focused mode a message that doesn't concern the agent never wakes its idle session:
// it goes to the start of its next turn, and is acknowledged once the session's next
// event confirms that turn had it.
func TestFocusedQuietMessageWaitsForTheTurnsStart(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	h := r.wait("s1", "b1", false)
	seq := r.broadcast(reviewer, "FYI: the build is green.")
	h.nothingFor("a quiet message")

	resp := r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if !strings.Contains(resp.Bundle, "while you were away") || !strings.Contains(resp.Bundle, `quiet="true"`) || !strings.Contains(resp.Bundle, "FYI: the build is green.") {
		t.Fatalf("the turn's start lacks the quiet message:\n%s", resp.Bundle)
	}
	if got := h.next(); got.Event != delivery.EventRelease {
		t.Fatalf("the turn's start should release the waiting hook, got %+v", got)
	}
	if r.server.Cursor(reviewer) != 0 {
		t.Fatal("acknowledged before the session's next event confirmed it")
	}
	r.wait("s1", "b1", false)
	r.eventually("the quiet message acknowledged", 0, func() bool { return r.server.Cursor(reviewer) == seq })
}

// A turn's start in all mode is given nothing; the message wakes the session instead.
func TestAllModeWakesForEveryMessage(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.setMode(reviewer, delivery.ModeAuto)
	if got := r.setMode(reviewer, ""); got.Mode != delivery.ModeAll {
		t.Fatalf("auto is saved as %q, want all", got.Mode)
	}
	r.broadcast(reviewer, "FYI: everyone.")
	if b := r.wait("s1", "b1", false).bundle(); !strings.Contains(b, "FYI: everyone.") || strings.Contains(b, "while you were away") {
		t.Fatalf("all mode should wake the session with the message in full:\n%s", b)
	}
}

// Messages for an idle session that arrive within QueueGather of each other go in one
// bundle.
func TestMessagesCloseTogetherWakeTheSessionOnce(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	h := r.wait("s1", "b1", false)
	r.post(reviewer, "one", false)
	r.clock.Advance(delivery.QueueGather / 2)
	r.post(reviewer, "two", false)
	if b := h.bundle(); !strings.Contains(b, `count="2"`) {
		t.Fatalf("want one bundle of both:\n%s", b)
	}
}
