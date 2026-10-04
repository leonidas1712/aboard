//go:build e2e

package e2e

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// The harness conformance kit's checks that a message the agent has received, by a
// confirmed delivery or by aboard inbox, is received everywhere: never delivered again,
// never named in a waiting notice again, never counted unread again (spec/delivery.md,
// "Received once"). They run for every harness, each through its own way of delivering:
// a hook that waits while idle, the harness's own queue, or an extension's connection.

// inboxSeqs runs aboard inbox in the session and returns the sequence numbers it showed.
func inboxSeqs(t *testing.T, s *kitSession) []int {
	t.Helper()
	var seqs []int
	for _, m := range field(t, s.run("inbox", "--json").json(t), "messages").([]any) {
		seqs = append(seqs, int(m.(map[string]any)["seq"].(float64)))
	}
	return seqs
}

// kitDeliveredIsRead checks that in the turn a bundle woke, before anything else of the
// session's confirms it, say's note doesn't count the delivered message as unread and
// aboard inbox doesn't show it again, and that it is never delivered again.
func kitDeliveredIsRead(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	seq := writerSays(t, e, "first, delivered")
	if b := s.nextBundle(w, 10*time.Second); !strings.Contains(b, `seq="`+strconv.Itoa(seq)+`"`) {
		t.Fatalf("the bundle lacks #%d:\n%s", seq, b)
	}

	said := s.run("say", "--to", "@writer", "--json", "On it.").json(t)
	if n := field(t, said, "unread.count"); n != float64(0) {
		t.Fatalf("say in the woken turn counts %v unread, want 0: #%d was delivered\n%v", n, seq, field(t, said, "unread"))
	}
	if got := inboxSeqs(t, s); len(got) != 0 {
		t.Fatalf("aboard inbox in the woken turn showed %v again", got)
	}

	w = s.idle()
	writerSays(t, e, "second")
	if b := s.nextBundle(w, 10*time.Second); strings.Contains(b, "first, delivered") || !strings.Contains(b, "second") {
		t.Fatalf("the next bundle should hold only the new message:\n%s", b)
	}
}

// kitReadMidTurnIsNotDelivered checks that messages the agent reads with aboard inbox
// while its turn runs never reach it again: not at a tool boundary, not in a waiting
// notice, not when the turn ends.
func kitReadMidTurnIsNotDelivered(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	if _, ok := p.Hook("prompt"); !ok && !p.Has("extension") {
		t.Skip("no prompt hook: the daemon can't tell a turn runs")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	if r := s.op("prompt", `"prompt":"a long task"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	owner := e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: read in the inbox")
	peer := writerSays(t, e, "peer: read in the inbox")

	// The agent catches up at a checkpoint of its turn. A peer's message may already be
	// in a queueing harness's queue, which counts as received, so inbox shows it at most
	// once; the owner's waits for a tool boundary everywhere.
	read := inboxSeqs(t, s)
	if !slices.Contains(read, owner) {
		t.Fatalf("aboard inbox showed %v, want the owner's #%d", read, owner)
	}
	if text := s.toolContext(t); text != "" {
		t.Fatalf("a tool boundary after aboard inbox added messages it had shown:\n%s", text)
	}
	w := s.idle()
	writerSays(t, e, "after the turn")
	var got string
	if s.ext != nil || p.WaitsForIdle() {
		got = s.nextBundle(w, 10*time.Second)
	} else {
		eventually(t, 10*time.Second, "the queue to take the message sent after the turn", func() bool {
			return strings.Contains(s.queuedText(), "after the turn")
		})
	}
	all := s.queuedText() + got
	if strings.Contains(all, "owner: read in the inbox") {
		t.Fatalf("the owner's message read with aboard inbox was delivered again:\n%s", all)
	}
	n := strings.Count(all, "peer: read in the inbox")
	if slices.Contains(read, peer) {
		n++
	}
	if n != 1 {
		t.Fatalf("the peer's #%d reached the agent %d times, want once (inbox showed %v):\n%s", peer, n, read, all)
	}
}

// kitReadBeforeConfirmed checks that a bundle handed to the session and never
// confirmed, whose messages the person then read with aboard inbox in a terminal, is
// never handed again, even to the next session for the agent.
func kitReadBeforeConfirmed(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	if !p.HoldsWhileBusy() {
		t.Skip("the harness's queue confirms a bundle as it takes it, so none waits unconfirmed")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	seq := writerSays(t, e, "read in a terminal")
	if s.ext != nil {
		// The extension took the bundle, and its harness went away before confirming it.
		s.ext.deliver(10*time.Second, false)
		_ = s.ext.conn.Close()
		e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
	} else if r := w.wait(10 * time.Second); r.code != 2 {
		t.Fatalf("the waiting hook should exit 2 with the bundle\n%s", r)
	}
	in := field(t, e.run("inbox", "--as", "reviewer", "--json").json(t), "messages").([]any)
	if len(in) != 1 || int(in[0].(map[string]any)["seq"].(float64)) != seq {
		t.Fatalf("aboard inbox in a terminal showed %v, want #%d", in, seq)
	}
	if s.ext == nil {
		s.op("end", "")
	}

	next := e.kitStart(p, kitID(), "startup", nil)
	next.run("resume", "reviewer")
	nw := next.idle()
	writerSays(t, e, "after the terminal read")
	if b := next.nextBundle(nw, 10*time.Second); strings.Contains(b, "read in a terminal") {
		t.Fatalf("a bundle read in a terminal was handed again:\n%s", b)
	}
}
