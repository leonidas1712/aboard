//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// start runs an aboard command inside the session in the background.
func (s *session) start(args ...string) *proc {
	s.e.t.Helper()
	cmd := exec.Command(s.e.bin, args...)
	cmd.Dir = s.e.dir
	cmd.Env = append(append([]string{}, s.e.vars...), s.vars...)
	p := &proc{t: s.e.t, cmd: cmd, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = p.out, p.errb
	if err := cmd.Start(); err != nil {
		s.e.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	s.e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	return p
}

// presenceIs waits until the board's members list shows the agent's presence and,
// unless mode is empty, its delivery mode.
func (e *env) presenceIs(board, agent, presence, mode string) {
	e.t.Helper()
	eventually(e.t, 10*time.Second, agent+" "+presence+" "+mode, func() bool {
		for _, m := range e.getAsOwner("/v1/boards/" + board + "/members")["members"].([]any) {
			m := m.(map[string]any)
			if m["name"] == agent {
				return m["presence"] == presence && (mode == "" || m["delivery"] == mode)
			}
		}
		return false
	})
}

// aboard say tells the sender what is waiting for it, and when each recipient will see
// the message, grouped by outcome, in text and in --json.
func TestSayTellsTheSenderWhatWaitsAndWhenRecipientsSeeIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := e.claudeSession("s-writer"), e.claudeSession("s-reviewer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	reviewer.run("join", line, "--name", "reviewer")
	third := e.claudeSession("s-third")
	third.run("join", line, "--name", "third")
	coder := e.codexSession("019a0000-0000-7000-8000-000000000031")
	coder.run("join", line, "--name", "coder")
	e.run("join", line, "--name", "bot") // in a terminal: no session
	e.run("delivery", "humans", "--as", "coder")

	reviewer.startHook("stop")
	third.hook("prompt", `"prompt":"long task"`)
	e.presenceIs("writer-reviewer", "reviewer", "idle", "auto")
	e.presenceIs("writer-reviewer", "third", "working", "auto")
	e.presenceIs("writer-reviewer", "coder", "idle", "humans")
	first := e.sayAs("bot", "--to", "@writer", "a question for the writer")
	second := e.sayAs("bot", "--to", "@writer", "and another")

	r := writer.run("say", "Everyone: the plan is in plan.md.")
	got := r.lines()
	if len(got) != 3 {
		t.Fatalf("say output:\n%s", r.stdout)
	}
	want := []string{
		got[0],
		"2 unread on writer-reviewer: #" + itoa(first) + ", #" + itoa(second) + "; run aboard inbox",
		"@reviewer gets it now. @third gets it when its turn ends. @coder won't be woken: it sees it when it checks its inbox. " +
			"@bot is disconnected: it sees it in its inbox or when its session reconnects. @alex sees it on the board or in their inbox.",
	}
	if !strings.HasPrefix(got[0], "Sent #") || !strings.HasSuffix(got[0], " to all on writer-reviewer") {
		t.Fatalf("first line: %s", got[0])
	}
	expectLines(t, r, want...)

	out := writer.run("say", "--to", "@coder,@bot", "--json", "Two of you.").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if field(t, out, "unread.count") != float64(2) || field(t, out, "unread.from.0.name") != "bot" || field(t, out, "unread.from.0.sender") != "owner_agent" {
		t.Fatalf("unread: %v", out["unread"])
	}
	outcomes := map[string]string{}
	for _, rc := range field(t, out, "recipients").([]any) {
		m := rc.(map[string]any)
		outcomes[m["name"].(string)] = m["outcome"].(string)
	}
	if len(outcomes) != 2 || outcomes["coder"] != "not_woken" || outcomes["bot"] != "no_session" {
		t.Fatalf("recipients: %v", out["recipients"])
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// aboard say --wait-reply returns the reply inside the same command and acknowledges
// it, so Codex's queue never gets it and nothing delivers it again.
func TestWaitReplyReturnsTheReplyInTheSameCommand(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000032")
	codex.run("join", line, "--name", "asker")
	codex.hook("prompt", `"prompt":"needs an answer"`)

	asking := codex.start("say", "--to", "@writer", "--wait-reply", "30", "--json", "Which port?")
	var seq int
	eventually(t, 10*time.Second, "the question to arrive", func() bool {
		msgs := field(t, writer.run("inbox", "--peek", "--json").json(t), "messages").([]any)
		if len(msgs) == 0 {
			return false
		}
		seq = int(msgs[0].(map[string]any)["seq"].(float64))
		return true
	})
	writer.run("say", "--reply", itoa(seq), "Port 7400.")
	r := asking.wait(15 * time.Second)
	if r.code != 0 {
		t.Fatalf("say --wait-reply failed\n%s", r)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, r)
	}
	matchesCLISpec(t, "SayOutput", out)
	if out["outcome"] != "reply" || field(t, out, "reply.body") != "Port 7400." || out["timed_out"] != false || field(t, out, "message.expects_reply") != true {
		t.Fatalf("wait-reply output: %v", out)
	}
	eventually(t, 5*time.Second, "the reply acknowledged", func() bool { return codex.unread() == 0 })
	codex.hook("stop", "")
	for _, c := range e.fakeCodexCalls() {
		if strings.Contains(c["message"], "Port 7400.") {
			t.Fatalf("the reply shown by say also went into Codex's queue:\n%s", c["message"])
		}
	}

	// The text form, in a Claude Code session: the reply in delivery format.
	reviewer := e.claudeSession("s-reviewer")
	reviewer.run("join", line, "--name", "reviewer")
	asking = reviewer.start("say", "--to", "@writer", "--wait-reply", "30", "Ready?")
	eventually(t, 10*time.Second, "the second question", func() bool {
		msgs := field(t, writer.run("inbox", "--peek", "--json").json(t), "messages").([]any)
		if len(msgs) == 0 {
			return false
		}
		seq = int(msgs[len(msgs)-1].(map[string]any)["seq"].(float64))
		return strings.Contains(msgs[len(msgs)-1].(map[string]any)["body"].(string), "Ready?")
	})
	writer.run("say", "--reply", itoa(seq), "Yes.")
	text := asking.wait(15 * time.Second)
	if text.code != 0 || !strings.Contains(text.stdout, "Reply from @writer:\n<aboard-message") || !strings.Contains(text.stdout, "Yes.") {
		t.Fatalf("text output\n%s", text)
	}
	if stop := reviewer.startHook("stop"); !stop.running(700 * time.Millisecond) {
		t.Fatalf("the reply was delivered again\n%s", stop.wait(time.Second))
	}
}

// A wait that runs out says the message was sent and not to send it again.
func TestWaitReplyTimeoutSaysNotToSendAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, _ := pairedClaudeSessions(t, e)
	r := writer.run("say", "--to", "@reviewer", "--wait-reply", "1", "Anyone?")
	last := r.lines()[len(r.lines())-1]
	if !strings.HasSuffix(last, "; no reply within 1 s. Don't send it again; a reply will reach you later.") || !strings.HasPrefix(last, "Sent #") {
		t.Fatalf("timeout text:\n%s", r.stdout)
	}
	out := writer.run("say", "--to", "@reviewer", "--wait-reply", "1", "--json", "Anyone at all?").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if out["outcome"] != "timeout" || out["timed_out"] != true || out["reply"] != nil {
		t.Fatalf("timeout JSON: %v", out)
	}
}

// When the owner writes during the wait, say --wait-reply returns that message in full;
// the peer's reply that comes later is delivered the normal way.
func TestWaitReplyReturnsTheOwnersMessage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	asking := writer.start("say", "--to", "@reviewer", "--wait-reply", "30", "--json", "Shall I start?")
	var seq int
	eventually(t, 10*time.Second, "the question to arrive", func() bool {
		msgs := field(t, reviewer.run("inbox", "--peek", "--json").json(t), "messages").([]any)
		if len(msgs) == 0 {
			return false
		}
		seq = int(msgs[0].(map[string]any)["seq"].(float64))
		return true
	})
	e.postAsOwnerTo("writer-reviewer", "@writer", "owner: stop and summarise first")
	r := asking.wait(15 * time.Second)
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || r.code != 0 {
		t.Fatalf("say --wait-reply\n%s", r)
	}
	matchesCLISpec(t, "SayOutput", out)
	if out["outcome"] != "owner_message" || field(t, out, "owner_messages.0.body") != "owner: stop and summarise first" || field(t, out, "owner_messages.0.sender") != "owner" {
		t.Fatalf("owner_message output: %v", out)
	}

	reviewer.run("say", "--reply", itoa(seq), "Go ahead.")
	woke := writer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "Go ahead.") || strings.Contains(woke.stderr, "summarise first") {
		t.Fatalf("the later reply should come in the normal way, without the owner's message again\n%s", woke)
	}
}
