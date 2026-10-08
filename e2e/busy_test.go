//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// toolContext is what a tool hook added to the running turn: the event it answered and
// its additionalContext, empty when it added nothing.
type toolContext struct {
	event, text string
}

// tool runs the session's tool hook once and returns what it added to the turn.
func (s *session) tool(t *testing.T, extra string) toolContext {
	t.Helper()
	r := s.hook("tool", extra)
	if r.code != 0 {
		t.Fatalf("tool hook failed\n%s", r)
	}
	return parseToolContext(t, r.stdout)
}

func parseToolContext(t *testing.T, stdout string) toolContext {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return toolContext{}
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			AdditionalContext  string `json:"additionalContext"`
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("tool hook output is not JSON: %v\n%s", err, stdout)
	}
	if out.Decision != "" || out.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("a tool hook must never block or decide on a tool call:\n%s", stdout)
	}
	return toolContext{event: out.HookSpecificOutput.HookEventName, text: out.HookSpecificOutput.AdditionalContext}
}

// ownerRequest makes an API request with this machine's person's login and returns the
// decoded answer, failing unless it succeeded.
func (e *env) ownerRequest(method, path string, body any) map[string]any {
	e.t.Helper()
	token, err := os.ReadFile(filepath.Join(e.configDir(), "local-owner-token"))
	if err != nil {
		e.t.Fatalf("no owner login: %v", err)
	}
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, "http://"+e.addr+path, payload)
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
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || resp.StatusCode >= 300 {
		e.t.Fatalf("%s %s: status %d, %v %v", method, path, resp.StatusCode, err, v)
	}
	return v
}

// postAsOwnerTo posts a message to one member as this machine's person and returns its
// sequence number.
func (e *env) postAsOwnerTo(board, to, body string) int {
	e.t.Helper()
	r := e.ownerRequest("POST", "/v1/boards/"+board+"/messages", map[string]any{"body": body, "to": []string{to}})
	return int(r["seq"].(float64))
}

// A message from the agent's owner reaches a busy Claude Code session at its next tool
// boundary, in full; peer messages, urgent ones too, wait for the end of the turn, and
// the hook only names them in a notice, once.
func TestOwnerReachesBusyClaudeSessionAtNextToolBoundary(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	e.run("delivery", "midturn", "owner-only")
	reviewer.hook("prompt", `"prompt":"long task"`)

	writer.run("say", "--to", "@reviewer", "ordinary peer note")                 // #6
	writer.run("say", "--to", "@reviewer", "--urgent", "peer says stop now")     // #7
	writer.run("say", "--to", "@reviewer", "--urgent", "peer says stop again")   // #8
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: switch to the docs") // #9

	var got toolContext
	eventually(t, 5*time.Second, "the tool hook to return the owner's message", func() bool {
		got = reviewer.tool(t, "")
		return strings.Contains(got.text, "owner: switch to the docs")
	})
	if got.event != "PostToolBatch" {
		t.Fatalf("the hook answered %q, want the event it ran for", got.event)
	}
	for _, want := range []string{
		"Aboard: your owner sent this while you were working",
		`sender="owner" seq="9"`,
		`<aboard-notice board="writer-reviewer" waiting="3">3 waiting on writer-reviewer: #6 from writer (owner_agent), #7 from writer (owner_agent), #8 from writer (owner_agent); run aboard inbox when convenient</aboard-notice>`,
	} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("tool context lacks %q:\n%s", want, got.text)
		}
	}
	for _, peer := range []string{"ordinary peer note", "peer says stop"} {
		if strings.Contains(got.text, peer) {
			t.Fatalf("a peer's words reached the busy turn:\n%s", got.text)
		}
	}

	// The next boundary repeats nothing; a new arrival is announced alone.
	if again := reviewer.tool(t, ""); again.text != "" {
		t.Fatalf("a later tool boundary repeated:\n%s", again.text)
	}
	writer.run("say", "--to", "@reviewer", "one more") // #10
	eventually(t, 5*time.Second, "a notice for the new message", func() bool {
		got = reviewer.tool(t, "")
		return got.text != ""
	})
	if !strings.Contains(got.text, `waiting="1">1 waiting on writer-reviewer: #10 from writer (owner_agent);`) || strings.Contains(got.text, "#6") {
		t.Fatalf("the notice should name only the new message:\n%s", got.text)
	}

	// At the end of the turn the peer messages arrive, urgent ones first in the order
	// they were sent, and the owner's message isn't repeated.
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || strings.Contains(woke.stderr, "switch to the docs") {
		t.Fatalf("the idle bundle should hold the peer messages only\n%s", woke)
	}
	first, second, ordinary := strings.Index(woke.stderr, "stop now"), strings.Index(woke.stderr, "stop again"), strings.Index(woke.stderr, "ordinary peer note")
	if first < 0 || second < first || ordinary < second {
		t.Fatalf("urgent messages should come first, in the order sent\n%s", woke.stderr)
	}
	reviewer.startHook("stop")
	eventually(t, 5*time.Second, "everything to be acknowledged", func() bool { return reviewer.unread() == 0 })
}

// The owner's message handed at a tool boundary counts as received only once the
// session shows a later event of that turn; a session that ends first gets it again in
// its next bundle.
func TestOwnerMessageMidTurnIsConfirmedByTheNextEvent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"long task"`)
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: check the tests")
	eventually(t, 5*time.Second, "the owner's message at a tool boundary", func() bool {
		return strings.Contains(reviewer.tool(t, "").text, "check the tests")
	})
	if n := reviewer.unread(); n != 1 {
		t.Fatalf("acknowledged before a later event: %d unread", n)
	}
	reviewer.hook("end", `"reason":"other"`)

	next := e.claudeSession("s-reviewer-2")
	next.run("resume", "reviewer")
	if woke := next.startHook("stop").wait(5 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "check the tests") {
		t.Fatalf("an unconfirmed mid-turn hand should be delivered again\n%s", woke)
	}
}

// A tool hook fired inside a sub-agent takes nothing; the root conversation's next tool
// boundary gets the owner's message.
func TestSubAgentToolHookTakesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"long task"`)
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: for the root conversation")
	if n := reviewer.unread(); n != 1 {
		t.Fatalf("the owner's message isn't unread: %d", n)
	}
	for range 3 {
		if got := reviewer.tool(t, `"agent_id":"sub-1","agent_type":"Explore"`); got.text != "" {
			t.Fatalf("a sub-agent's tool hook took:\n%s", got.text)
		}
	}
	eventually(t, 5*time.Second, "the root's tool hook to get it", func() bool {
		return strings.Contains(reviewer.tool(t, "").text, "for the root conversation")
	})
}

// Tool calls made at once fire their hooks at the same time; each message is claimed by
// one of them and the notice is given once.
func TestParallelToolHooksClaimEachMessageOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000021")
	codex.run("join", line, "--name", "reviewer")
	e.run("delivery", "midturn", "owner-only")
	codex.hook("prompt", `"prompt":"long task"`)

	writer.run("say", "--to", "@reviewer", "peer note")
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: one claim only")

	const parallel = 6
	var (
		mu      sync.Mutex
		outputs []toolContext
		wg      sync.WaitGroup
	)
	procs := make([]*proc, parallel)
	for i := range procs {
		procs[i] = codex.startHook("tool")
	}
	for _, p := range procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := p.wait(10 * time.Second)
			mu.Lock()
			defer mu.Unlock()
			outputs = append(outputs, parseToolContext(t, r.stdout))
		}()
	}
	wg.Wait()
	owner, notices := 0, 0
	for _, o := range outputs {
		if strings.Contains(o.text, "owner: one claim only") {
			owner += strings.Count(o.text, "owner: one claim only")
			if o.event != "PreToolUse" {
				t.Fatalf("Codex's tool hook answered %q", o.event)
			}
		}
		notices += strings.Count(o.text, "<aboard-notice")
	}
	if owner != 1 || notices > 1 {
		t.Fatalf("owner's message given %d times, notices %d, want 1 and at most 1: %+v", owner, notices, outputs)
	}
	if calls := e.fakeCodexCalls(); len(calls) != 0 {
		t.Fatalf("parallel boundaries queued messages during the busy turn: %v", calls)
	}
	stop := e.exec(codex.vars, hookInput(codex.id, "Stop", ""), "hook", "codex", "stop-continue")
	out := stop.json(t)
	reason, _ := out["reason"].(string)
	if stop.code != 0 || out["decision"] != "block" || strings.Count(reason, "peer note") != 1 || strings.Contains(reason, "owner: one claim only") {
		t.Fatalf("Stop should deliver the waiting peer once, without the claimed owner\n%s", stop)
	}
	if again := codex.tool(t, ""); again.text != "" {
		t.Fatalf("a later boundary repeated parallel or Stop context: %+v", again)
	}
	eventually(t, 5*time.Second, "both parallel and Stop deliveries acknowledged", func() bool { return codex.unread() == 0 })
	complete := e.exec(codex.vars, hookInput(codex.id, "Stop", `"stop_hook_active":true`), "hook", "codex", "stop-continue")
	if complete.code != 0 || strings.TrimSpace(complete.stdout) != "" {
		t.Fatalf("Stop repeated an acknowledged parallel delivery\n%s", complete)
	}
	if calls := e.fakeCodexCalls(); len(calls) != 0 {
		t.Fatalf("Stop also queued a parallel delivery: %v", calls)
	}
}

// During a Codex turn the owner's message goes to the next tool call instead of Codex's
// queue, where it would wait for the turn to end. Under owner-only policy, peer
// messages wait for a fresh Stop continuation, which confirms the tool delivery.
func TestOwnerReachesBusyCodexSessionBeforeNextToolCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000003")
	codex.run("join", line, "--name", "reviewer")
	e.run("delivery", "midturn", "owner-only")
	codex.hook("prompt", `"prompt":"long task"`)

	writer.run("say", "--to", "@reviewer", "--urgent", "peer says stop")
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner says stop")
	var got toolContext
	eventually(t, 5*time.Second, "the tool hook to return the owner's message", func() bool {
		got = codex.tool(t, `"tool_name":"Bash"`)
		return strings.Contains(got.text, "owner says stop")
	})
	if got.event != "PreToolUse" || strings.Contains(got.text, "peer says stop") {
		t.Fatalf("tool context: %+v", got)
	}
	if calls := e.fakeCodexCalls(); len(calls) != 0 {
		t.Fatalf("a busy turn received external queue entries: %v", calls)
	}
	if again := codex.tool(t, `"tool_name":"Bash"`); again.text != "" {
		t.Fatalf("a later tool boundary repeated context: %+v", again)
	}
	stop := e.exec(codex.vars, hookInput(codex.id, "Stop", ""), "hook", "codex", "stop-continue")
	var continuation struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stop.stdout), &continuation); err != nil || stop.code != 0 || continuation.Decision != "block" {
		t.Fatalf("Codex Stop did not continue with the waiting message: %v\n%s", err, stop)
	}
	if strings.Count(continuation.Reason, "peer says stop") != 1 || strings.Contains(continuation.Reason, "owner says stop") {
		t.Fatalf("Stop should deliver the peer exactly once, without repeating the owner:\n%s", stop)
	}
	// Codex resumes from the Stop reason and runs another tool, confirming receipt.
	if again := codex.tool(t, `"tool_name":"Bash"`); again.text != "" {
		t.Fatalf("the continuation repeated delivered context: %+v", again)
	}
	complete := e.exec(codex.vars, hookInput(codex.id, "Stop", `"stop_hook_active":true`), "hook", "codex", "stop-continue")
	if complete.code != 0 || strings.TrimSpace(complete.stdout) != "" {
		t.Fatalf("the next Stop should end without another continuation\n%s", complete)
	}
	if calls := e.fakeCodexCalls(); len(calls) != 0 {
		t.Fatalf("Stop continuation also used the external queue: %v", calls)
	}
	eventually(t, 5*time.Second, "both messages acknowledged", func() bool { return codex.unread() == 0 })
}

// An owner's message too long for one tool boundary is shown cut short, once, with the
// way to read it all; it stays unread and arrives whole when the turn ends.
func TestLongOwnerMessageIsCutShortMidTurnAndArrivesWholeLater(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"long task"`)
	body := "owner: long instructions " + strings.Repeat("abcdefghij", 1200) + " THE END"
	e.postAsOwnerTo("writer-reviewer", "@reviewer", body)

	var got toolContext
	eventually(t, 5*time.Second, "the cut-short message", func() bool {
		got = reviewer.tool(t, "")
		return got.text != ""
	})
	if len([]rune(got.text)) >= 10000 || !strings.Contains(got.text, `truncated="true"`) ||
		!strings.Contains(got.text, "run aboard inbox to read it now") || strings.Contains(got.text, "THE END") {
		t.Fatalf("tool context (%d characters):\n%.600s", len([]rune(got.text)), got.text)
	}
	if again := reviewer.tool(t, ""); again.text != "" {
		t.Fatalf("the cut-short message was shown again:\n%.300s", again.text)
	}
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "THE END") {
		t.Fatalf("the whole message should arrive when the turn ends\n%.300s", woke.stderr)
	}
}
