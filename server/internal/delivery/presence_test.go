package delivery_test

import (
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// presence waits until the server's latest presence for agent is want.
func (r *rig) presence(agent delivery.AgentRef, want delivery.Presence) {
	r.t.Helper()
	r.eventually(agent.Name+" "+string(want), 0, func() bool { return r.server.Presence(agent) == want })
}

// hookCall sends one hook request for the Claude Code session s1, boot b1.
func (r *rig) hookCall(op string) {
	r.t.Helper()
	r.ok(delivery.Request{Op: op, Harness: "claude-code", Session: "s1", Boot: "b1"})
}

// A Claude Code session's agent is idle while the session waits, working while a turn
// runs, and has no session once it ends.
func TestClaudeSessionReportsItsAgentsPresence(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)

	r.hookCall(delivery.OpPrompt)
	r.presence(reviewer, delivery.PresenceWorking)
	h := r.wait("s1", "b1", false)
	r.presence(reviewer, delivery.PresenceIdle)

	// A message wakes the waiting session: its agent works on it.
	r.post(reviewer, "please look", false)
	h.bundle()
	r.presence(reviewer, delivery.PresenceWorking)
	r.hookCall(delivery.OpUrgent)
	r.wait("s1", "b1", false)
	r.presence(reviewer, delivery.PresenceIdle)

	r.hookCall(delivery.OpEnd)
	r.presence(reviewer, delivery.PresenceNoSession)
}

func TestCodexTurnsReportPresence(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "codex", Session: "t1"})
	r.presence(reviewer, delivery.PresenceWorking)
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "t1"})
	r.presence(reviewer, delivery.PresenceIdle)
}

// A harness killed without its end hook leaves its agent with no session.
func TestDeadHarnessLeavesItsAgentWithNoSession(t *testing.T) {
	r := newRig(t)
	harness := delivery.Process{PID: 7201, Start: 1759320000}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s1", Boot: "b1", Process: &harness})
	r.bind("claude-code", "s1", reviewer)
	r.hookCall(delivery.OpPrompt)
	r.presence(reviewer, delivery.PresenceWorking)
	r.procs.Kill(harness)
	r.eventually("no session", delivery.LivenessCheck, func() bool { return r.server.Presence(reviewer) == delivery.PresenceNoSession })
}

// Replacing the session's seat on one board leaves that seat with no session.
func TestMovedSessionLeavesTheOldAgentWithNoSession(t *testing.T) {
	r := newRig(t)
	replacement := planner
	replacement.Board = reviewer.Board
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	r.bind("claude-code", "s1", replacement)
	r.presence(replacement, delivery.PresenceIdle)
	r.presence(reviewer, delivery.PresenceNoSession)
}

// The daemon renews a presence well before the server lets it run out, and doesn't
// renew no_session, which is what an unrenewed presence becomes anyway.
func TestPresenceIsRenewedWhileItHolds(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	reports := r.server.PresenceReports(reviewer)
	r.eventually("a renewal", delivery.PresenceRenew, func() bool { return r.server.PresenceReports(reviewer) > reports })
	if delivery.PresenceRenew*2 >= 3*time.Minute {
		t.Fatalf("renewing every %s leaves too little room before the server's 3 minutes", delivery.PresenceRenew)
	}

	r.hookCall(delivery.OpEnd)
	r.presence(reviewer, delivery.PresenceNoSession)
	ended := r.server.PresenceReports(reviewer)
	for range 3 {
		r.clock.Advance(delivery.PresenceRenew)
	}
	r.hookCall(delivery.OpAgents) // a round trip through the session after the renewals
	if got := r.server.PresenceReports(reviewer); got != ended {
		t.Fatalf("no_session was reported %d more times", got-ended)
	}
}
