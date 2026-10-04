//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// A reply without --to goes to the asker and the others already in the thread, and say
// names who it went to and when they see it; a third agent on the board isn't woken.
// --to all still reaches everyone, and a reply with no one else in its thread asks for
// a target.
func TestReplyGoesToTheAskerAndDoesNotWakeOthers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	third := e.claudeSession("s-third")
	line := field(t, e.run("invite", "--board", "writer-reviewer", "--json").json(t), "join_line").(string)
	third.run("join", line, "--name", "third")

	asker := writer.startHook("stop")
	bystander := third.startHook("stop")
	e.presenceIs("writer-reviewer", "writer", "idle", "focused")
	e.presenceIs("writer-reviewer", "third", "idle", "focused")
	ask := e.sayAs("writer", "--to", "@reviewer", "--expect-reply", "Which port?")

	r := reviewer.run("say", "--reply", itoa(ask), "Port 7400.")
	if last := r.lines()[len(r.lines())-1]; last != "@writer gets it now." {
		t.Fatalf("say should say the asker gets it now:\n%s", r.stdout)
	}
	if !strings.HasPrefix(r.lines()[0], "Sent #") || !strings.HasSuffix(r.lines()[0], " to @writer on writer-reviewer") {
		t.Fatalf("the reply should go to the asker:\n%s", r.stdout)
	}
	woke := asker.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "Port 7400.") {
		t.Fatalf("the reply should wake the asker\n%s", woke)
	}
	if !bystander.running(asleepFor) {
		t.Fatalf("a reply to someone else woke a third agent\n%s", bystander.wait(time.Second))
	}
	if n := third.unread(); n != 0 {
		t.Fatalf("the third agent has %d unread: the reply wasn't addressed to it", n)
	}

	out := reviewer.run("say", "--reply", itoa(ask), "--to", "all", "--json", "Everyone: it's 7400.").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if to := field(t, out, "message.to").([]any); len(to) != 1 || to[0] != "all" {
		t.Fatalf("--to all on a reply went to %v", to)
	}

	own := e.sayAs("third", "Heads up: the build is slow today.")
	r = third.runExit("say", "--reply", itoa(own), "--json", "Still slow.")
	if r.code == 0 || field(t, r.json(t), "error.code") != "reply_has_no_recipients" ||
		!strings.Contains(field(t, r.json(t), "error.hint").(string), "--to all") {
		t.Fatalf("a reply with no one else in its thread should ask for a target\n%s", r)
	}
}
