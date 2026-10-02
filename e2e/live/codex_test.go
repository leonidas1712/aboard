//go:build live

package live

import (
	"testing"
	"time"
)

// An idle Codex session is woken through codex queue when a message arrives, and
// answers on the board with no one typing.
func TestIdleCodexWakesAndReplies(t *testing.T) {
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	reviewer := l.startCodex("reviewer", l.project("project", "codex"))
	reviewer.bind("reviewer")

	ping := l.say("writer", "--to", "@reviewer", "--expect-reply", "Reply to this message with exactly PONG-1.")
	wake := l.waitHanded(ping.At, 30*time.Second)
	reply := l.waitMessage("reviewer", ping.At, "PONG-1", 3*time.Minute)
	t.Logf("measured: queued %s after posting, reply on the board %s after posting",
		wake.Time.Sub(ping.At), reply.At.Sub(ping.At))
	// Codex gathers messages for 2 seconds before queueing them.
	if d := wake.Time.Sub(ping.At); d > wakeBound+2*time.Second {
		t.Errorf("the message was queued for Codex %s after it was posted; want within %s", d, wakeBound+2*time.Second)
	}
}

// After one prompt to Claude Code, it and Codex run the skill's wiring check to PING 3:
// six messages go back and forth with no one typing, and then they stop.
func TestClaudeAndCodexExchange(t *testing.T) {
	requireClaude(t)
	codex := requireCodex(t)
	t.Parallel()
	l := newLab(t)
	// Every process in the lab, Claude Code's hooks and the daemon they start included,
	// needs the scratch CODEX_HOME, so it is set before anything starts.
	l.codexHome(codex)
	l.pairCLI()
	writer := l.startClaude("writer", l.project("claude-project", "claude-code"))
	reviewer := l.startCodex("reviewer", l.project("codex-project", "codex"))
	writer.bind("writer")
	reviewer.bind("reviewer")

	start := time.Now()
	writer.submit("Run the Aboard wiring check with @reviewer now, going up to PING 3 instead of PING 2.")
	l.waitMessage("reviewer", start, "PONG 3", 6*time.Minute)
	l.waitQuiet(3*time.Minute, "reviewer", writer, reviewer)
	exchangedAlone(t, l.messages("reviewer"), start, 5, 8)
}
