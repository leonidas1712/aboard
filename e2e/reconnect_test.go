//go:build e2e

package e2e

import (
	"bytes"
	"os/exec"
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

// A resumed Claude Code session wakes for a message before anyone types: its second
// session-start hook (resume-wait) waits from the moment it opens, as the stop hook does
// after a turn. Claude Code runs it beside the hook that registers the session, so it may
// reach the daemon first; it asks again until the register has arrived. A prompt
// releases it, as it releases a waiting stop hook.
func TestResumedClaudeSessionWakesBeforeItsFirstTurn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("end", `"reason":"prompt_input_exit"`)

	resumeWait := func() *proc {
		cmd := exec.Command(e.bin, "hook", "claude-code", "resume-wait")
		cmd.Dir = e.dir
		cmd.Env = append([]string{}, e.vars...)
		cmd.Stdin = strings.NewReader(hookInput(reviewer.id, "SessionStart", `"source":"resume"`))
		p := &proc{t: t, cmd: cmd, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, done: make(chan error, 1)}
		cmd.Stdout, cmd.Stderr = p.out, p.errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { p.done <- cmd.Wait() }()
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return p
	}

	early := resumeWait()
	back := e.claudeSessionFrom(reviewer.id, "resume")
	writer.run("say", "--to", "@reviewer", "sent after the resume, before any prompt")
	if got := early.wait(10 * time.Second); got.code != 2 || !strings.Contains(got.stderr, "sent after the resume, before any prompt") {
		t.Fatalf("the resumed session should wake before its first turn\n%s", got)
	}
	back.startHook("stop")
	eventually(t, 5*time.Second, "the message to be acknowledged", func() bool { return back.unread() == 0 })

	back.hook("end", `"reason":"prompt_input_exit"`)
	e.claudeSessionFrom(reviewer.id, "resume")
	released := resumeWait()
	back.hook("prompt", `"prompt":"hello"`)
	if got := released.wait(5 * time.Second); got.code != 0 || strings.TrimSpace(got.stderr) != "" {
		t.Fatalf("a prompt should release the resume waiter quietly\n%s", got)
	}
}

// A resumed Claude Code session with nothing waiting to wake it (a Claude Code too old
// for the resume waiter, or a waiting hook that ended) is reported not_waiting once it
// has been idle like that for a few seconds: status says so, and say tells the sender
// the agent sees the message at its next turn rather than now. The warning clears once
// a hook waits again.
func TestIdleSessionWithNothingWaitingIsReported(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"hello"`)
	reviewer.hook("end", `"reason":"prompt_input_exit"`)
	back := e.claudeSessionFrom(reviewer.id, "resume")

	warning := func() string {
		w, _ := field(t, e.run("status", "--as", "reviewer", "--json").json(t), "delivery_warning").(string)
		return w
	}
	eventually(t, 15*time.Second, "reviewer to be reported not_waiting", func() bool { return warning() == "not_waiting" })
	if r := e.run("status", "--as", "reviewer"); !strings.Contains(r.stdout, "nothing waits to wake it") {
		t.Fatalf("status doesn't explain the warning:\n%s", r)
	}
	if r := e.runExit("doctor"); !strings.Contains(r.stdout, "reviewer on writer-reviewer: its session is idle, but nothing waits to wake it") ||
		!strings.Contains(r.stdout, "type anything in that session, or quit it and resume it") {
		t.Fatalf("doctor doesn't report the session nothing waits on:\n%s", r)
	}
	if r := writer.run("say", "--to", "@reviewer", "while nothing waits"); !strings.Contains(r.stdout, "@reviewer sees it at its next turn.") {
		t.Fatalf("say should tell the sender the reviewer sees it at its next turn:\n%s", r)
	}

	back.startHook("stop")
	eventually(t, 10*time.Second, "the warning to clear once a hook waits", func() bool { return warning() == "" })
}
