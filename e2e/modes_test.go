//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// postAsOwner posts a message on a board as this machine's person, with the local owner
// login, the way the web UI or a script on the API would.
func (e *env) postAsOwner(board, body string, urgent bool) {
	e.t.Helper()
	token, err := os.ReadFile(filepath.Join(e.configDir(), "local-owner-token"))
	if err != nil {
		e.t.Fatalf("no owner login: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"body": body, "to": []string{"@reviewer"}, "urgent": urgent})
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+e.addr+"/v1/boards/"+board+"/messages", bytes.NewReader(payload))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("owner post: status %d", resp.StatusCode)
	}
}

// An agent nobody set delivers in auto mode, and both aboard delivery and aboard status
// say so, inside the session too.
func TestDeliveryModeIsAutoByDefault(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)

	expectLines(t, reviewer.run("delivery"), "reviewer on writer-reviewer: delivery auto (wakes for every message)")
	out := reviewer.run("delivery", "--json").json(t)
	if field(t, out, "mode") != "auto" || field(t, out, "changed") != false || field(t, out, "agent") != "reviewer" || field(t, out, "board") != "writer-reviewer" {
		t.Fatalf("delivery --json: %v", out)
	}
	if got := field(t, reviewer.run("status", "--json").json(t), "delivery"); got != "auto" {
		t.Fatalf("status delivery %v, want auto", got)
	}
}

// In humans mode a peer's message doesn't wake the session; a person's message does,
// and its bundle carries the peer message that waited, oldest first.
func TestHumansModeWakesOnlyForPeople(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	expectLines(t, e.run("delivery", "humans", "--as", "reviewer"),
		"reviewer on writer-reviewer: delivery now humans (wakes only for messages from people)")
	if got := field(t, reviewer.run("status", "--json").json(t), "delivery"); got != "humans" {
		t.Fatalf("status delivery %v, want humans", got)
	}
	if !strings.Contains(reviewer.run("status").stdout, "Agent:  reviewer (from this session); delivery humans;") {
		t.Fatalf("status text lacks the mode:\n%s", reviewer.run("status").stdout)
	}

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "peer note")
	if !stop.running(500 * time.Millisecond) {
		t.Fatalf("a peer message woke the session in humans mode\n%s", stop.wait(time.Second))
	}

	e.postAsOwner("writer-reviewer", "owner asks for a summary", false)
	woke := stop.wait(5 * time.Second)
	if woke.code != 2 {
		t.Fatalf("the owner's message should wake the session\n%s", woke)
	}
	for _, want := range []string{`count="2"`, "peer note", `sender="owner_agent"`, "owner asks for a summary", `sender="owner"`} {
		if !strings.Contains(woke.stderr, want) {
			t.Fatalf("bundle lacks %q\n%s", want, woke)
		}
	}
	if strings.Index(woke.stderr, "peer note") > strings.Index(woke.stderr, "owner asks") {
		t.Fatalf("bundle is not oldest first\n%s", woke.stderr)
	}
	reviewer.startHook("stop")
	eventually(t, 5*time.Second, "both messages to be acknowledged", func() bool { return reviewer.unread() == 0 })
}

// In humans mode an urgent peer message doesn't interrupt a busy turn; an urgent message
// from a person does, alone, and the peer one waits for the next bundle.
func TestHumansModeUrgentPeerMessageDoesNotInterrupt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	e.run("delivery", "humans", "--as", "reviewer")
	reviewer.hook("prompt", `"prompt":"long task"`)

	writer.run("say", "--to", "@reviewer", "--urgent", "peer says stop")
	e.postAsOwner("writer-reviewer", "owner says stop", true)
	var context string
	eventually(t, 5*time.Second, "the tool hook to return the owner's urgent message", func() bool {
		context = reviewer.hook("tool", `"tool_name":"Bash"`).stdout
		return strings.Contains(context, "owner says stop")
	})
	if strings.Contains(context, "peer says stop") {
		t.Fatalf("an urgent peer message interrupted the turn in humans mode:\n%s", context)
	}
	if r := reviewer.hook("tool", `"tool_name":"Bash"`); r.stdout != "" {
		t.Fatalf("a later tool call got more messages:\n%s", r.stdout)
	}
}

// In off mode nothing is delivered, from peers or people; the agent reads its inbox.
func TestOffModeDeliversNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	out := e.run("delivery", "off", "--as", "reviewer", "--json").json(t)
	if field(t, out, "mode") != "off" || field(t, out, "changed") != true {
		t.Fatalf("delivery off --json: %v", out)
	}
	if again := e.run("delivery", "off", "--as", "reviewer", "--json").json(t); field(t, again, "changed") != false {
		t.Fatalf("setting the same mode again: %v", again)
	}

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "peer note")
	e.postAsOwner("writer-reviewer", "owner note", false)
	if !stop.running(500 * time.Millisecond) {
		t.Fatalf("a message woke the session in off mode\n%s", stop.wait(time.Second))
	}
	inbox := reviewer.run("inbox").stdout
	for _, want := range []string{"peer note", "owner note"} {
		if !strings.Contains(inbox, want) {
			t.Fatalf("inbox lacks %q:\n%s", want, inbox)
		}
	}
	reviewer.hook("prompt", `"prompt":"next"`)
	if r := stop.wait(5 * time.Second); r.code != 0 {
		t.Fatalf("the stop hook should be released by a prompt\n%s", r)
	}

	// Back to auto: the next message wakes the session again.
	e.run("delivery", "auto", "--as", "reviewer")
	stop = reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "back on")
	if woke := stop.wait(5 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "back on") {
		t.Fatalf("auto mode should wake the session again\n%s", woke)
	}
}

// Changing the mode is a person's decision: inside a harness session it refuses and
// says to use a terminal. Showing it works anywhere.
func TestDeliveryModeCannotBeChangedInsideASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)

	r := reviewer.runExit("delivery", "off", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("changing the mode inside a session should refuse\n%s", r)
	}
	if hint := field(t, r.json(t), "error.hint").(string); !strings.Contains(hint, "terminal") {
		t.Fatalf("hint should say to use a terminal: %s", hint)
	}
	// Claude Code sets CLAUDECODE for every command it runs, even without Aboard's hooks.
	if r := e.exec([]string{"CLAUDECODE=1"}, "", "delivery", "off", "--as", "reviewer", "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("changing the mode with CLAUDECODE set should refuse\n%s", r)
	}
	codex := &session{e: e, harness: "codex", id: "019a", vars: []string{"CODEX_THREAD_ID=019a"}}
	if r := codex.runExit("delivery", "humans", "--as", "reviewer", "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("changing the mode inside Codex should refuse\n%s", r)
	}
	expectLines(t, reviewer.run("delivery"), "reviewer on writer-reviewer: delivery auto (wakes for every message)")
}

// Commands that act or read with the person's login refuse inside a harness session,
// where an allow rule for aboard would let an agent run them without asking. Pairing and
// verifying the record still work there.
func TestHumanCommandsRefuseInsideASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	writer.run("pair", "writer-reviewer")
	writer.run("audit", "verify")

	codex := &session{e: e, harness: "codex", id: "019a", vars: []string{"CODEX_THREAD_ID=019a"}}
	for _, s := range []*session{writer, codex} {
		// The hint is the exact command for the agent to hand to its person, naming the
		// board even when the agent didn't.
		for _, c := range []struct {
			args    []string
			command string
		}{
			{[]string{"board", "policy", "recommended", "--json"}, "aboard board policy recommended --board writer-reviewer"},
			{[]string{"watch", "--json"}, "aboard watch --board writer-reviewer"},
		} {
			r := s.runExit(c.args...)
			if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
				t.Fatalf("%s %v should refuse inside a session\n%s", s.harness, c.args, r)
			}
			if hint := field(t, r.json(t), "error.hint").(string); !strings.Contains(hint, "terminal") || !strings.HasSuffix(hint, ": "+c.command) {
				t.Fatalf("hint should hand over %q for a terminal: %s", c.command, hint)
			}
		}
	}
	if got := field(t, e.run("board", "policy", "recommended", "--json").json(t), "after.preset"); got != "recommended" {
		t.Fatalf("board policy in a terminal: %v", got)
	}
}
