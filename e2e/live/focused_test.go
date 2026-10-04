//go:build live

package live

import (
	"slices"
	"strconv"
	"testing"
	"time"
)

// Focused delivery in the real harnesses (spec/delivery.md, "Delivery modes"): what
// doesn't concern an agent never wakes it and arrives with its owner's next prompt, and
// a reply without --to wakes the asker and no one else.

// quietWindow is how long a scenario watches an idle session to see a quiet message
// doesn't wake it: well past the daemon's gathering and a harness's wake.
const quietWindow = 15 * time.Second

// Another agent's message to everyone, asking nothing, doesn't wake an idle session in
// the default focused mode. When the owner's next prompt starts a turn, the harness's
// turn-start mechanism adds it before the model runs, so the agent can answer from it,
// and it is acknowledged.
func TestQuietMessageArrivesWithTheOwnersNextPrompt(t *testing.T) {
	eachHarness(t, "QuietArrivesWithTheNextPrompt", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Has("turn-start") {
			rec.notApplicable("the profile declares no turn-start capability: quiet messages wait for the agent's aboard inbox")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")
		if d.startsAtFirstTurn {
			writer.submit("Reply only OK.")
			writer.waitIdle(2 * time.Minute)
		}

		fyi := l.say("reviewer", "FYI for everyone: the release is tagged QUIET-4217.")
		l.neverWithin(quietWindow, "the message to everyone woke the idle session", func() bool {
			_, _, ok := l.reached(fyi.Seq)
			return ok
		})

		start := time.Now()
		writer.submit(`Which release tag did the Aboard messages you just got mention? Answer by running: aboard say --to @reviewer "TAG <the tag>". Run no other aboard command.`)
		l.waitMessage("writer", start, "TAG QUIET-4217", 4*time.Minute)
		writer.waitIdle(2 * time.Minute)
		at, how, ok := l.reached(fyi.Seq)
		t.Logf("measured: the quiet message reached the session %s after the prompt (%s)", at.Sub(start), how)
		if !ok || how != "turn start" {
			t.Fatalf("the quiet message reached the session by %q; want at the start of the owner's turn", how)
		}
		l.waitFor(time.Minute, "the quiet message to be acknowledged", func() bool {
			return !slices.ContainsFunc(l.writerInbox(), func(m message) bool { return m.Seq == fyi.Seq })
		})
	})
}

// A session asks a question; the answer, sent with --reply and no --to, goes to the
// asker only: it wakes the asker's session, which acts on it, and a third agent's idle
// session on the same board is never woken.
func TestReplyWakesOnlyTheAsker(t *testing.T) {
	eachHarness(t, "ReplyWakesOnlyTheAsker", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		l := newLab(t)
		d.setUp(l)
		board := l.pairCLI()
		var invite struct {
			JoinLine string `json:"join_line"`
		}
		l.decode(l.human, &invite, "invite", "--board", board, "--json")
		l.run("join", invite.JoinLine, "--name", "third", "--json")
		writer := d.start(l, "writer", l.project("writer-project", d.p.Harness))
		writer.bind("writer")
		third := d.start(l, "third", l.project("third-project", d.p.Harness))
		third.bind("third")
		if d.startsAtFirstTurn {
			for _, p := range []*pane{writer, third} {
				p.submit("Reply only OK.")
				p.waitIdle(2 * time.Minute)
			}
		}
		thirdStates := l.watchPresence("third")

		start := time.Now()
		writer.submit(`Ask @reviewer on Aboard which port the server uses: run aboard say --to @reviewer --expect-reply "Which port?" and end your turn. When the answer arrives, run aboard say --to @reviewer "GOT <the port>".`)
		ask := l.waitMessage("writer", start, "Which port", 4*time.Minute)
		writer.waitIdle(2 * time.Minute)
		reply := l.say("reviewer", "--reply", strconv.Itoa(ask.Seq), "Port 7400.")
		if !slices.Equal(reply.To, []string{"@writer"}) {
			t.Fatalf("the reply went to %v; want the asker, [@writer]", reply.To)
		}
		got := l.waitMessage("writer", reply.At, "GOT 7400", 4*time.Minute)
		t.Logf("measured: the asker acted on the reply %s after it was posted", got.At.Sub(reply.At))
		// Every bundle since the reply went to one session, the asker's.
		l.neverWithin(quietWindow, "a bundle was handed to a second session, the third agent's", func() bool {
			sessions := map[string]bool{}
			for _, h := range l.handedAfter(reply.At) {
				sessions[h.Session] = true
			}
			return len(sessions) > 1
		})
		if states := thirdStates(); slices.Contains(states, "working") {
			t.Errorf("the third agent's presence went %v; a reply to someone else woke it", states)
		}
	})
}
