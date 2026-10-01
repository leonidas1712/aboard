//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pairedClaudeSessions makes two Claude Code sessions, pairs them on one board (writer in
// the first, reviewer in the second) and returns them.
func pairedClaudeSessions(t *testing.T, e *env) (writer, reviewer *session) {
	t.Helper()
	writer, reviewer = e.claudeSession("s-writer"), e.claudeSession("s-reviewer")
	line := field(t, writer.run("pair", "--json").json(t), "join.line").(string)
	reviewer.run("join", line)
	return writer, reviewer
}

// An idle Claude Code session wakes with the message, and the message is acknowledged on
// the board once the session's next event confirms the woken turn ran.
func TestIdleClaudeSessionWakesWithMessage(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	stop := reviewer.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatalf("stop hook returned without a message\n%s", stop.wait(time.Second))
	}
	// No --as: each session's bound agent decides who is speaking.
	expectLines(t, writer.run("say", "--to", "@reviewer", "Draft is in notes.md."), "Sent #6 to @reviewer on writer-reviewer")

	woke := stop.wait(5 * time.Second)
	if woke.code != 2 {
		t.Fatalf("stop hook should exit 2 with the bundle\n%s", woke)
	}
	for _, want := range []string{`<aboard-messages board="writer-reviewer" count="1">`, `from="@writer"`, `trust="peer"`, "Draft is in notes.md."} {
		if !strings.Contains(woke.stderr, want) {
			t.Fatalf("bundle on stderr lacks %q\n%s", want, woke)
		}
	}
	if n := reviewer.unread(); n != 1 {
		t.Fatalf("acknowledged before the session confirmed: %d unread", n)
	}

	// The woken turn ends: Claude Code runs the stop hook again, which confirms.
	again := reviewer.startHook("stop")
	eventually(t, 5*time.Second, "the board to acknowledge the delivered message", func() bool { return reviewer.unread() == 0 })
	if r := reviewer.hook("prompt", `"prompt":"hi"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	if r := again.wait(5 * time.Second); r.code != 0 {
		t.Fatalf("a prompt should release the waiting stop hook with exit 0\n%s", r)
	}
}

// Messages that arrive while a session is busy arrive together, as one bundle, when it
// is next idle.
func TestBusyClaudeSessionGetsOneBundleWhenIdle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"work on something"`)

	for _, body := range []string{"one", "two", "three"} {
		writer.run("say", "--to", "@reviewer", body)
	}
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `count="3"`) {
		t.Fatalf("want one bundle of 3\n%s", woke)
	}
	if strings.Index(woke.stderr, "one") > strings.Index(woke.stderr, "three") {
		t.Fatalf("bundle is not oldest first\n%s", woke.stderr)
	}
}

// An urgent message reaches a busy session at its next tool call; ordinary ones wait.
func TestUrgentMessageReachesBusyClaudeSessionAtNextToolCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.hook("prompt", `"prompt":"long task"`)

	writer.run("say", "--to", "@reviewer", "ordinary note")
	writer.run("say", "--to", "@reviewer", "--urgent", "stop: the build is broken")

	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	eventually(t, 5*time.Second, "the tool hook to return the urgent message", func() bool {
		r := reviewer.hook("tool", `"tool_name":"Bash"`)
		return r.code == 0 && json.Unmarshal([]byte(r.stdout), &out) == nil && out.HookSpecificOutput.AdditionalContext != ""
	})
	ctx := out.HookSpecificOutput.AdditionalContext
	if out.HookSpecificOutput.HookEventName != "PostToolUse" || !strings.Contains(ctx, "the build is broken") || !strings.Contains(ctx, `urgent="true"`) {
		t.Fatalf("tool hook output: %+v", out)
	}
	if strings.Contains(ctx, "ordinary note") {
		t.Fatalf("an ordinary message was delivered mid-turn:\n%s", ctx)
	}

	// At idle the ordinary message arrives, and the urgent one isn't repeated.
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "ordinary note") || strings.Contains(woke.stderr, "the build is broken") {
		t.Fatalf("idle bundle should hold only the ordinary message\n%s", woke)
	}
}

// A bundle the session never confirmed is delivered again to the next session that
// takes over the agent.
func TestUnconfirmedBundleGoesToTheNextSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "please review")
	if woke := stop.wait(5 * time.Second); woke.code != 2 {
		t.Fatalf("no wake\n%s", woke)
	}
	// The session crashes before its turn ends.
	reviewer.hook("end", `"reason":"other"`)

	next := e.claudeSession("s-reviewer-2")
	expectLines(t, next.run("resume", "reviewer"), "Resumed reviewer on writer-reviewer in this session.")
	woke := next.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "please review") || !strings.Contains(woke.stderr, `seq="6"`) {
		t.Fatalf("the unconfirmed bundle should be delivered again, with the same seq\n%s", woke)
	}
}

// If the daemon dies while a stop hook waits, the hook brings it back and still delivers.
func TestStopHookSurvivesDaemonRestart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	stop := reviewer.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatal("stop hook returned early")
	}

	pidFile := filepath.Join(e.home, ".local", "state", "aboard", "daemon.pid")
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("no daemon pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	writer.run("say", "--to", "@reviewer", "still there?")
	if woke := stop.wait(10 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "still there?") {
		t.Fatalf("delivery after a daemon crash\n%s", woke)
	}
}

// A Codex session receives messages through codex queue, bundled, and they are
// acknowledged once Codex accepts them.
func TestCodexSessionReceivesMessagesThroughItsQueue(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000001")
	codex.run("join", line)

	writer.run("say", "--to", "@reviewer", "first")
	writer.run("say", "--to", "@reviewer", "second")
	eventually(t, 10*time.Second, "codex queue to receive the messages", func() bool {
		var all string
		for _, c := range e.fakeCodexCalls() {
			all += c["message"]
		}
		return strings.Contains(all, "first") && strings.Contains(all, "second")
	})
	for _, c := range e.fakeCodexCalls() {
		if c["thread"] != codex.id {
			t.Fatalf("queued to thread %q, want %q", c["thread"], codex.id)
		}
		if !strings.Contains(c["message"], "<aboard-messages") {
			t.Fatalf("queued text is not a bundle:\n%s", c["message"])
		}
	}
	eventually(t, 5*time.Second, "acknowledgement after codex accepted", func() bool { return codex.unread() == 0 })
}

// A Codex sub-agent thread can't join: messages must go to the root conversation.
func TestCodexSubAgentThreadCannotJoin(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "--json").json(t), "join.line").(string)
	threads := `{"019a0000-0000-7000-8000-00000000000b":{"parent":"019a0000-0000-7000-8000-00000000000a"}}`
	if err := os.WriteFile(filepath.Join(e.home, "fake-codex-threads.json"), []byte(threads), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := &session{
		e: e, harness: "codex", id: "019a0000-0000-7000-8000-00000000000b",
		vars: []string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-00000000000b"},
	}
	r := sub.runExit("join", line, "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "codex_subagent_target" {
		t.Fatalf("a sub-agent thread joined\n%s", r)
	}
	status := e.run("status", "--as", "writer", "--json").json(t)
	if agents := field(t, status, "agents").([]any); len(agents) != 1 {
		t.Fatalf("a sub-agent joined anyway: agents %v", agents)
	}
}

// The acting agent decides the board: a name used on two boards needs --board.
func TestAgentNameOnTwoBoardsMustBeDisambiguated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair")
	e.run("pair", "--new")
	other := filepath.Join(e.home, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	e.dir = other // a directory with no .aboard file

	r := e.runExit("say", "--as", "writer", "hello", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "agent_ambiguous" {
		t.Fatalf("want agent_ambiguous\n%s", r)
	}
	boards := field(t, r.json(t), "error.details.boards").([]any)
	if len(boards) != 2 {
		t.Fatalf("details.boards %v", boards)
	}
	expectLines(t, e.run("say", "--as", "writer", "--board", "writer-reviewer-2", "hello"), "Sent #5 to all on writer-reviewer-2")
}

// aboard init, with --yes, installs the skill and hooks for detected harnesses, and a
// second run changes nothing.
func TestInitInstallsSkillAndHooksOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan := e.run("init", "--json").json(t)
	if field(t, plan, "applied") != false {
		t.Fatal("init without --yes changed files")
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("init without --yes wrote Claude Code settings")
	}

	e.run("init", "--yes")
	settings, err := os.ReadFile(filepath.Join(e.home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"aboard hook claude-code session-start", "aboard hook claude-code stop", "asyncRewake", "aboard hook claude-code tool"} {
		if !strings.Contains(string(settings), want) {
			t.Fatalf("Claude Code settings lack %q:\n%s", want, settings)
		}
	}
	if _, err := os.Stat(filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")); err != nil {
		t.Fatalf("Claude Code skill not installed: %v", err)
	}

	again := e.run("init", "--yes", "--json").json(t)
	for _, h := range field(t, again, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if c.(map[string]any)["action"] != "unchanged" {
				t.Fatalf("second init changed %v", c)
			}
		}
	}
}

// aboard doctor reports a running daemon and missing hooks with a fix.
func TestDoctorReportsEachPart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	// Claude Code counts as installed when ~/.claude exists, whatever is on the PATH.
	if err := os.MkdirAll(filepath.Join(e.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.claudeSession("s1") // starts the daemon
	r := e.runExit("doctor", "--json")
	checks := map[string]map[string]any{}
	for _, c := range field(t, r.json(t), "checks").([]any) {
		m := c.(map[string]any)
		checks[m["name"].(string)] = m
	}
	if d, ok := checks["daemon"]; !ok || d["level"] != "ok" {
		t.Fatalf("daemon check %v\n%s", d, r)
	}
	if h, ok := checks["claude_hooks"]; !ok || h["code"] != "claude_hooks_missing" || h["fix"] == nil {
		t.Fatalf("claude_hooks check %v\n%s", h, r)
	}
	if r.code != 3 {
		t.Fatalf("doctor with an error check should exit 3\n%s", r)
	}
}

// When a stop hook wakes Claude Code, Claude Code submits the hook's output as the next
// prompt, so the prompt hook fires with the bundle as its text. That prompt is the wake
// itself, not a later event: if the session dies before doing anything else, the bundle
// must still go to the next session.
func TestWakePromptDoesNotConfirmTheBundle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "please review section 2")
	woke := stop.wait(5 * time.Second)
	if woke.code != 2 {
		t.Fatalf("no wake\n%s", woke)
	}
	prompt, err := json.Marshal(woke.stderr)
	if err != nil {
		t.Fatal(err)
	}
	if r := reviewer.hook("prompt", `"prompt":`+string(prompt)); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	if n := reviewer.unread(); n != 1 {
		t.Fatalf("the wake prompt confirmed the bundle: %d unread", n)
	}
	reviewer.hook("end", `"reason":"other"`)

	next := e.claudeSession("s-reviewer-2")
	next.run("resume", "reviewer")
	again := next.startHook("stop").wait(5 * time.Second)
	if again.code != 2 || !strings.Contains(again.stderr, "please review section 2") {
		t.Fatalf("the bundle should be delivered again\n%s", again)
	}
}

// openSessions is how many sessions the delivery daemon has open, as aboard doctor
// reports it.
func (e *env) openSessions() string {
	e.t.Helper()
	for _, c := range field(e.t, e.runExit("doctor", "--json").json(e.t), "checks").([]any) {
		m := c.(map[string]any)
		if m["name"] == "daemon" {
			msg := m["message"].(string)
			return msg[strings.LastIndex(msg, ", ")+2:]
		}
	}
	e.t.Fatal("doctor has no daemon check")
	return ""
}

// A session whose harness was killed, so its end hook never ran, is closed once the
// daemon sees the process gone; that lets the daemon stop when nothing else is open.
func TestSessionWhoseHarnessDiedIsClosed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "--json").json(t), "join.line").(string)
	reviewer, harness := e.claudeSessionIn("s-reviewer")
	reviewer.run("join", line)
	if got := e.openSessions(); got != "2 sessions" {
		t.Fatalf("before the crash: %s", got)
	}

	if err := harness.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = harness.Wait()
	eventually(t, 15*time.Second, "the dead session to close", func() bool { return e.openSessions() == "1 session" })
}
