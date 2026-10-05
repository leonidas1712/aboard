package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// modeIs waits until the daemon applies mode to the agent.
func (r *rig) modeIs(agent delivery.AgentRef, mode delivery.Mode) {
	r.t.Helper()
	r.eventually("the daemon to apply "+string(mode), 0, func() bool { return r.setMode(agent, "").Mode == mode })
}

// The mode the agent's server holds wins over one kept on this machine, and the daemon
// follows a change made on the server: from another machine or the board view. The next
// bundle says the mode changed.
func TestTheServersModeWinsAndIsFollowed(t *testing.T) {
	r := newRig(t)
	r.setMode(reviewer, delivery.ModeAll)
	r.server.SetHeldMode(reviewer, delivery.ModeOff)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.modeIs(reviewer, delivery.ModeOff)

	h := r.wait("s1", "b1", false)
	r.post(reviewer, "@reviewer, please look", false)
	h.nothingFor("a message in off mode, set on the server")

	r.server.SetHeldMode(reviewer, delivery.ModeAll)
	b := h.bundle()
	if line := deliverytext.ModeChanged("docs", "off", "all"); !strings.HasPrefix(b, line) || !strings.Contains(b, "please look") {
		t.Fatalf("the bundle after the change should start with the changed-mode line:\n%s", b)
	}
	if got := r.server.Mode(reviewer); got != delivery.ModeAll {
		r.eventually("the daemon to report all with its presence", 0, func() bool { return r.server.Mode(reviewer) == delivery.ModeAll })
	}
}

// A wake already queued for a busy Codex session is dropped when the agent's person
// turns delivery off: nothing is handed once the session can take it. It goes when the
// mode allows it again.
func TestOffDropsAWakeAlreadyQueued(t *testing.T) {
	r := newRig(t)
	r.server.HoldModes()
	r.bind("codex", "t1", reviewer)
	r.modeIs(reviewer, delivery.ModeFocused)
	r.codex.SetBusy(true)
	r.post(reviewer, "@reviewer, the tests fail", false)
	r.eventually("the busy answers", time.Second, func() bool { return r.codex.Attempts() >= 2 })

	r.server.SetHeldMode(reviewer, delivery.ModeOff)
	r.modeIs(reviewer, delivery.ModeOff)
	r.codex.SetBusy(false)
	tried := r.codex.Attempts()
	for range 10 {
		r.clock.Advance(time.Second)
		<-time.After(5 * time.Millisecond) // a poll interval, not a wait for the daemon
	}
	if got := r.codex.Handed("t1"); len(got) != 0 || r.codex.Attempts() != tried {
		t.Fatalf("off should drop the queued wake: handed %q after %d attempts", got, r.codex.Attempts()-tried)
	}

	r.server.SetHeldMode(reviewer, delivery.ModeFocused)
	r.eventually("the delivery once the mode allows it", time.Second, func() bool { return len(r.codex.Handed("t1")) == 1 })
	// The session was never told off, so a mode changed and changed back says nothing.
	if got := r.codex.Handed("t1")[0]; !strings.Contains(got, "the tests fail") || strings.Contains(got, "delivery mode") {
		t.Fatalf("handed %q", got)
	}
}

// A message that would wake an idle session, still being gathered when the mode turns
// off, is never handed.
func TestOffStopsAWakeBeingGathered(t *testing.T) {
	r := newRig(t)
	r.server.HoldModes()
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.modeIs(reviewer, delivery.ModeFocused)
	h := r.wait("s1", "b1", false)
	r.post(reviewer, "@reviewer, quick question", false)
	r.server.SetHeldMode(reviewer, delivery.ModeOff)
	h.nothingFor("a wake gathered before the mode turned off")
}

// A wake queued for an agent's peer message is dropped when its person picks humans,
// and the peer message goes with the next person's message.
func TestHumansDropsAQueuedWakeForAPeerMessage(t *testing.T) {
	r := newRig(t)
	r.server.HoldModes()
	r.bind("codex", "t1", reviewer)
	r.modeIs(reviewer, delivery.ModeFocused)
	r.codex.SetBusy(true)
	r.post(reviewer, "peer asks for review", false)
	r.eventually("the busy answers", time.Second, func() bool { return r.codex.Attempts() >= 2 })

	r.server.SetHeldMode(reviewer, delivery.ModeHumans)
	r.modeIs(reviewer, delivery.ModeHumans)
	r.codex.SetBusy(false)
	for range 10 {
		r.clock.Advance(time.Second)
		<-time.After(5 * time.Millisecond) // a poll interval, not a wait for the daemon
	}
	if got := r.codex.Handed("t1"); len(got) != 0 {
		t.Fatalf("humans mode should drop the queued wake for a peer message: %q", got)
	}
	r.postFromOwner(reviewer, "alex says go ahead")
	r.eventually("the person's message", time.Second, func() bool { return len(r.codex.Handed("t1")) == 1 })
	if got := r.codex.Handed("t1")[0]; !strings.Contains(got, "peer asks for review") || !strings.Contains(got, "alex says go ahead") {
		t.Fatalf("the person's message should bring the peer one:\n%s", got)
	}
}

// Reads of the server can arrive out of order; an older revision never replaces a newer
// one, whether it comes from a read or from the command that set the mode.
func TestAnOlderRevisionNeverReplacesANewerOne(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	rev := r.server.SetHeldMode(reviewer, delivery.ModeHumans)
	r.modeIs(reviewer, delivery.ModeHumans)
	old := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &reviewer, Mode: delivery.ModeAll, Revision: int64(rev - 1)})
	if old.Mode != delivery.ModeHumans || old.Changed {
		t.Fatalf("an older revision was taken: %+v", old)
	}
	newer := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &reviewer, Mode: delivery.ModeOff, Revision: int64(rev + 1)})
	if newer.Mode != delivery.ModeOff || !newer.Changed {
		t.Fatalf("a newer revision from the command that set it: %+v", newer)
	}
	// A mode set on this machine only doesn't change what the server holds.
	if local := r.setMode(reviewer, delivery.ModeAll); local.Mode != delivery.ModeOff || local.Changed {
		t.Fatalf("a mode kept on this machine won over the server's: %+v", local)
	}
}

// A mode the server holds is kept in the journal, so a restarted daemon starts with it
// before it reads the server again.
func TestTheServersModeIsKeptAcrossRestarts(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.server.SetHeldMode(reviewer, delivery.ModeHumans)
	r.modeIs(reviewer, delivery.ModeHumans)
	r.restart()
	if got := r.setMode(reviewer, ""); got.Mode != delivery.ModeHumans {
		t.Fatalf("after a restart: %+v", got)
	}
}
