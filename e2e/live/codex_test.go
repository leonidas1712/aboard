//go:build live

package live

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	wake := l.waitHanded(ping.At)
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

// Codex starts the skill's wiring check itself. Each PONG must reach Codex promptly,
// measured from the daemon's log, not only be posted: Codex either waits for it with
// aboard say --wait-reply or ends its turn so delivery can bring it, and never keeps
// its turn busy polling.
func TestCodexStartsPingPong(t *testing.T) {
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	reviewer := l.startCodex("reviewer", l.project("project", "codex"))
	reviewer.bind("reviewer")

	start := time.Now()
	reviewer.submit("Run the Aboard wiring check with @writer now.")
	for n := 1; n <= 2; n++ {
		ping := l.waitMessage("reviewer", start, fmt.Sprintf("PING %d", n), 4*time.Minute)
		pong := l.say("writer", "--reply", strconv.Itoa(ping.Seq), fmt.Sprintf("PONG %d", n))
		var at time.Time
		var how string
		l.waitFor(2*time.Minute, fmt.Sprintf("PONG %d to reach Codex", n), func() bool {
			var ok bool
			at, how, ok = l.reached(pong.Seq)
			return ok
		})
		t.Logf("measured: PONG %d reached Codex %s after it was posted (%s)", n, at.Sub(pong.At), how)
		if d := at.Sub(pong.At); d > pongBound {
			t.Errorf("PONG %d reached Codex %s after it was posted; want within %s. A reply sat waiting while Codex kept its turn busy", n, d, pongBound)
		}
	}
	l.waitFor(time.Minute, "PONG 2 to be acknowledged", func() bool { return len(l.inbox("reviewer")) == 0 })
	reviewer.waitIdle(2 * time.Minute)
}

// pongBound is how soon a reply must reach a session that asked for it: Codex's turn
// after posting a PING is short, then its queue starts the next one after a 2-second
// gather.
const pongBound = 30 * time.Second

// While Codex runs a slow task, a message from its owner (posted with the owner login on
// the API) reaches the turn before the next tool call and is acted on in that turn; it
// never goes into Codex's queue, which would hold it until the turn ends.
func TestOwnerReachesBusyCodex(t *testing.T) {
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	board := l.pairCLI()
	proj := l.project("project", "codex")
	writeSlowTask(t, proj, 23)
	reviewer := l.startCodex("reviewer", proj)
	reviewer.bind("reviewer")

	reviewer.submit("Run `./" + slowTask + "` in its own command and wait for it. When it has finished, run `./" + slowTask +
		"` again in a second, separate command and wait for it. Then reply DONE.")
	l.waitFor(2*time.Minute, "Codex to run the slow task", func() bool { return pgrep("sleep 23") })
	own := l.postAsOwner(board, "@reviewer", `OWNER: run aboard say "OWNER-ACK" right away, then carry on with your task.`)
	ack := l.waitMessage("reviewer", own.At, "OWNER-ACK", 3*time.Minute)
	at, how, ok := l.reached(own.Seq)
	reviewer.waitIdle(3 * time.Minute)
	t.Logf("measured: the owner's message reached Codex %s after posting (%s), OWNER-ACK %s after posting", at.Sub(own.At), how, ack.At.Sub(own.At))
	if !ok || how != "tool boundary" {
		t.Fatalf("the owner's message reached Codex by %q; want at a tool boundary of the busy turn", how)
	}
	for _, h := range l.logged("bundle handed") {
		if slices.Contains(h.Seqs, own.Seq) {
			t.Errorf("the owner's message also went into Codex's queue at %s", h.Time)
		}
	}
	l.waitFor(30*time.Second, "the owner's message to be acknowledged", func() bool { return len(l.inbox("reviewer")) == 0 })
}

// Codex asks with aboard say --wait-reply and gets the reply inside the same command,
// from inside its sandbox: the reply is shown by the command, never queued for a later
// turn.
func TestCodexWaitsForReplyInItsTurn(t *testing.T) {
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	reviewer := l.startCodex("asker", l.project("project", "codex"))
	reviewer.bind("reviewer")

	start := time.Now()
	reviewer.submit(`Run: aboard say --to @writer --wait-reply 120 "PING W". When it prints the reply, run: aboard say "GOT" followed by the reply's text.`)
	ping := l.waitMessage("reviewer", start, "PING W", 3*time.Minute)
	pong := l.say("writer", "--reply", strconv.Itoa(ping.Seq), "PONG W")
	got := l.waitMessage("reviewer", pong.At, "GOT", 3*time.Minute)
	reviewer.waitIdle(2 * time.Minute)
	at, how, ok := l.reached(pong.Seq)
	t.Logf("measured: the reply reached Codex %s after it was posted (%s); GOT %s after", at.Sub(pong.At), how, got.At.Sub(pong.At))
	if !ok || how != "claimed by a command" {
		t.Fatalf("the reply reached Codex by %q; want shown by aboard say --wait-reply", how)
	}
	if !strings.Contains(got.Body, "PONG W") {
		t.Errorf("Codex reported %q; want the reply's text", got.Body)
	}
	for _, h := range l.logged("bundle handed") {
		if slices.Contains(h.Seqs, pong.Seq) {
			t.Errorf("the reply was also queued for Codex at %s", h.Time)
		}
	}
	l.waitFor(30*time.Second, "the reply to be acknowledged", func() bool { return len(l.inbox("reviewer")) == 0 })
}
