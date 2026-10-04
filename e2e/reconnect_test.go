//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// A Claude Code session that closes and is resumed with the same session id (claude
// --resume) fills its seat again by itself: no aboard resume. It shows as disconnected
// while closed, the bundle it never confirmed and the message that waited both arrive,
// and the session-start hook tells the agent who it is again.
func TestResumedSessionReconnectsByItself(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "handed before the close")
	if woke := stop.wait(5 * time.Second); woke.code != 2 {
		t.Fatalf("no wake\n%s", woke)
	}
	// The person quits before the woken turn ends, so nothing confirmed the bundle.
	reviewer.hook("end", `"reason":"prompt_input_exit"`)
	eventually(t, 5*time.Second, "the reviewer to be disconnected", func() bool {
		p, _ := field(t, e.run("status", "--as", "reviewer", "--json").json(t), "presence").(string)
		return p == "no_session"
	})
	if r := e.run("status", "--as", "reviewer"); !strings.Contains(r.stdout, "; delivery focused; disconnected\n") {
		t.Fatalf("the Agent line doesn't say disconnected:\n%s", r)
	}
	expectLines(t, writer.run("say", "--to", "@reviewer", "sent while closed"),
		"Sent #7 to @reviewer on writer-reviewer",
		"@reviewer is disconnected: it sees it in its inbox or when its session reconnects.")

	back := e.claudeSessionFrom(reviewer.id, "resume")
	if !strings.Contains(back.started.stdout, "Aboard: this session is reviewer on writer-reviewer again") {
		t.Fatalf("the session-start hook didn't say the session is reviewer again\n%s", back.started)
	}
	if got := field(t, back.run("status", "--json").json(t), "agent"); got != "reviewer" {
		t.Fatalf("the resumed session acts as %v, want reviewer", got)
	}
	got := back.startHook("stop").wait(5 * time.Second)
	if got.code != 2 || !strings.Contains(got.stderr, "handed before the close") || !strings.Contains(got.stderr, `seq="6"`) {
		t.Fatalf("the resumed session should get the bundle it never confirmed again\n%s", got)
	}
	got = back.startHook("stop").wait(5 * time.Second)
	if got.code != 2 || !strings.Contains(got.stderr, "sent while closed") {
		t.Fatalf("the resumed session should get the message that waited\n%s", got)
	}
	back.startHook("stop")
	eventually(t, 5*time.Second, "both messages to be acknowledged", func() bool { return back.unread() == 0 })
}

// Only the exact session id reconnects. A new session in the same place is not bound,
// and a session whose agent another session resumed while it was closed doesn't take
// it back: it starts with no agent and its session-start hook says so.
func TestOnlyTheSameSessionReconnects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("end", "")

	fresh := e.claudeSession("s-new")
	if strings.TrimSpace(fresh.started.stdout) != "" {
		t.Fatalf("a new session's start hook printed something\n%s", fresh.started)
	}
	if got := field(t, fresh.run("status", "--json").json(t), "agent"); got != nil {
		t.Fatalf("a new session was bound to %v by itself", got)
	}

	other := e.claudeSession("s-other")
	other.run("resume", "reviewer")
	back := e.claudeSessionFrom(reviewer.id, "resume")
	for _, want := range []string{
		"Aboard: this session was reviewer on writer-reviewer until another session resumed reviewer; it has no agent now.",
		"aboard resume reviewer",
	} {
		if !strings.Contains(back.started.stdout, want) {
			t.Fatalf("the session-start hook doesn't say %q\n%s", want, back.started)
		}
	}
	if got := field(t, back.run("status", "--json").json(t), "agent"); got != nil {
		t.Fatalf("the old session took back %v", got)
	}

	stop := other.startHook("stop")
	writer.run("say", "--to", "@reviewer", "for the session that resumed it")
	if got := stop.wait(5 * time.Second); got.code != 2 || !strings.Contains(got.stderr, "for the session that resumed it") {
		t.Fatalf("the session that resumed reviewer should keep getting its messages\n%s", got)
	}
}

// A Codex thread opened again with codex resume keeps its thread id, so it reconnects
// the same way and the message that waited goes into its queue.
func TestResumedCodexThreadReconnects(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000002")
	codex.run("join", line, "--name", "reviewer")
	codex.hook("end", "")

	writer.run("say", "--to", "@reviewer", "sent while the thread was closed")
	if calls := e.fakeCodexCalls(); len(calls) != 0 {
		t.Fatalf("a closed thread was queued a bundle: %v", calls)
	}
	e.codexSessionFrom(codex.id, "resume")
	eventually(t, 10*time.Second, "the resumed thread to be queued the message", func() bool {
		for _, c := range e.fakeCodexCalls() {
			if c["thread"] == codex.id && strings.Contains(c["message"], "sent while the thread was closed") {
				return true
			}
		}
		return false
	})
}
