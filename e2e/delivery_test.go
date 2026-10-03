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
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	reviewer.run("join", line, "--name", "reviewer")
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
	expectLines(t, writer.run("say", "--to", "@reviewer", "Draft is in notes.md."), "Sent #6 to @reviewer on writer-reviewer", "@reviewer gets it now.")

	woke := stop.wait(5 * time.Second)
	if woke.code != 2 {
		t.Fatalf("stop hook should exit 2 with the bundle\n%s", woke)
	}
	for _, want := range []string{`<aboard-messages board="writer-reviewer" count="1">`, `from="@writer"`, `harness="claude-code" sender="owner_agent"`, "Draft is in notes.md."} {
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

// A session fills one seat at a time: joining another board moves it there. The old
// agent's later messages wait for whichever session resumes it, and the bundle handed
// for it before the move is handed again there, so nothing is lost.
func TestJoiningAnotherBoardMovesTheSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	line := field(t, e.run("pair", "writer-reviewer", "--new", "--json").json(t), "join.line").(string)

	stop := reviewer.startHook("stop")
	writer.run("say", "--to", "@reviewer", "handed before the move")
	if woke := stop.wait(5 * time.Second); woke.code != 2 {
		t.Fatalf("no wake\n%s", woke)
	}
	// In the woken turn, before anything confirms the bundle, the session joins another board.
	moved := reviewer.run("join", line, "--name", "sweeper")
	if !strings.Contains(moved.stdout, "This session was reviewer on writer-reviewer; it is now sweeper on writer-reviewer-2.") {
		t.Fatalf("join didn't say the session moved\n%s", moved)
	}
	if got := field(t, reviewer.run("status", "--json").json(t), "agent"); got != "sweeper" {
		t.Fatalf("status in the session shows agent %v, want sweeper", got)
	}
	again := reviewer.run("resume", "sweeper", "--json").json(t)
	if prev := field(t, again, "previous_agent"); prev != nil {
		t.Fatalf("resuming the agent the session already holds moved it from %v", prev)
	}

	writer.run("say", "--to", "@reviewer", "for the old seat")
	e.run("say", "--as", "writer", "--board", "writer-reviewer-2", "--to", "@sweeper", "for the new seat")
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "for the new seat") ||
		strings.Contains(woke.stderr, "for the old seat") || strings.Contains(woke.stderr, "handed before the move") {
		t.Fatalf("the moved session should get only the new seat's messages\n%s", woke)
	}

	next := e.claudeSession("s-reviewer-2")
	resumed := next.run("resume", "reviewer", "--json").json(t)
	if prev := field(t, resumed, "previous_agent"); prev != nil {
		t.Fatalf("a session with no agent moved from %v", prev)
	}
	// The bundle handed before the move goes again as it was, then what came after it.
	got := next.startHook("stop").wait(5 * time.Second)
	if got.code != 2 || !strings.Contains(got.stderr, "handed before the move") {
		t.Fatalf("resuming the old agent should hand its unconfirmed bundle again\n%s", got)
	}
	got = next.startHook("stop").wait(5 * time.Second)
	if got.code != 2 || !strings.Contains(got.stderr, "for the old seat") {
		t.Fatalf("resuming the old agent should deliver what came after the move\n%s", got)
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

	pidFile := filepath.Join(e.stateDir(), "daemon.pid")
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
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-000000000001")
	codex.run("join", line, "--name", "reviewer")

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
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
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
	e.run("pair", "writer-reviewer")
	e.run("pair", "writer-reviewer", "--new")
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
	expectLines(t, e.run("say", "--as", "writer", "--board", "writer-reviewer-2", "hello"), "Sent #5 to all on writer-reviewer-2", "@alex sees it on the board or in their inbox.")
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
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	reviewer, harness := e.claudeSessionIn("s-reviewer")
	reviewer.run("join", line, "--name", "reviewer")
	if got := e.openSessions(); got != "2 sessions" {
		t.Fatalf("before the crash: %s", got)
	}

	if err := harness.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = harness.Wait()
	eventually(t, 15*time.Second, "the dead session to close", func() bool { return e.openSessions() == "1 session" })
}

// The race between a turn's stop hook and the next prompt: the user types while the
// previous turn's stop hook is still starting, so the prompt reaches the daemon first and
// the stop hook's wait arrives late. That late wait must not count as idle, or a bundle
// would go to a session that is busy.
func TestLateStopHookAfterAPromptGetsNoBundle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	late, connect := reviewer.startHookHeld("stop")
	// Make sure the hook process has started before the prompt is submitted, as it has
	// in Claude Code, where the stop hook starts when the turn ends.
	if !late.running(200 * time.Millisecond) {
		t.Fatalf("held stop hook exited\n%s", late.wait(time.Second))
	}
	if r := reviewer.hook("prompt", `"prompt":"next task"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	connect()
	if r := late.wait(5 * time.Second); r.code != 0 {
		t.Fatalf("a stop hook that started before the prompt should be released\n%s", r)
	}

	writer.run("say", "--to", "@reviewer", "for the next idle moment")
	if n := reviewer.unread(); n != 1 {
		t.Fatalf("the message was handed to a busy session: %d unread", n)
	}
	woke := reviewer.startHook("stop").wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "for the next idle moment") {
		t.Fatalf("the turn's own stop hook should get the message\n%s", woke)
	}
}

// daemonRunning reports whether this env's delivery daemon is running.
func (e *env) daemonRunning() bool {
	raw, err := os.ReadFile(filepath.Join(e.stateDir(), "daemon.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return err == nil && syscall.Kill(pid, 0) == nil
}

// Inside a harness's sandbox, a command that needs the delivery daemon doesn't start
// one there, where it couldn't reach the harness; it says how to fix it instead.
func TestCommandInsideASandboxDoesNotStartTheDaemon(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	if e.daemonRunning() {
		t.Fatal("pair outside any session started the daemon")
	}
	sandboxed := &session{
		e: e, harness: "codex", id: "019a0000-0000-7000-8000-000000000004",
		vars: []string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-000000000004", "CODEX_SANDBOX=seatbelt"},
	}

	r := sandboxed.runExit("join", line, "--json")
	if r.code == 0 || field(t, r.json(t), "error.code") != "daemon_in_sandbox" {
		t.Fatalf("join inside a sandbox should fail with daemon_in_sandbox\n%s", r)
	}
	hint := field(t, r.json(t), "error.hint").(string)
	for _, want := range []string{"aboard daemon start", "hooks"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint should mention %q: %s", want, hint)
		}
	}
	if e.daemonRunning() {
		t.Fatal("a daemon was started inside the sandbox")
	}

	checks := map[string]map[string]any{}
	for _, c := range field(t, sandboxed.runExit("doctor", "--json").json(t), "checks").([]any) {
		m := c.(map[string]any)
		checks[m["name"].(string)] = m
	}
	if d := checks["daemon"]; d["code"] != "daemon_in_sandbox" || !strings.Contains(d["fix"].(string), "aboard daemon start") {
		t.Fatalf("doctor's daemon check inside a sandbox: %v", d)
	}
	if e.daemonRunning() {
		t.Fatal("doctor started a daemon inside the sandbox")
	}

	if r := sandboxed.runExit("daemon", "start"); r.code == 0 {
		t.Fatalf("daemon start inside a sandbox should refuse\n%s", r)
	}
	e.run("daemon", "start")
	if !e.daemonRunning() {
		t.Fatal("aboard daemon start in a normal terminal didn't start the daemon")
	}
	sandboxed.run("join", line)
}

// aboard status shows whether the local server and the delivery daemon run, without
// starting them; aboard down stops both.
func TestStatusShowsWhatRunsAndDownStopsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	session := e.claudeSession("s-writer") // starts the daemon
	session.run("pair", "writer-reviewer")

	st := session.run("status", "--json").json(t)
	if field(t, st, "server_running") != true || field(t, st, "daemon.running") != true || field(t, st, "daemon.open_sessions") != float64(1) {
		t.Fatalf("status before down: %v", st)
	}
	text := session.run("status").stdout
	for _, want := range []string{"Server: http://127.0.0.1:" + e.port() + " running", "Daemon: running (pid ", "1 open session"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status lacks %q:\n%s", want, text)
		}
	}

	expectLines(t, e.run("down"), "Stopped local Aboard at http://127.0.0.1:"+e.port()+" and the delivery daemon.")
	if e.daemonRunning() {
		t.Fatal("the daemon still runs after aboard down")
	}
	st = e.run("status", "--json").json(t)
	if field(t, st, "server_running") != false || field(t, st, "daemon.running") != false {
		t.Fatalf("status after down: %v", st)
	}
	text = e.run("status").stdout
	for _, want := range []string{"not running; aboard up starts it", "Daemon: not running"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status lacks %q:\n%s", want, text)
		}
	}
	if e.daemonRunning() {
		t.Fatal("aboard status started the daemon")
	}
	if r := e.run("down", "--json").json(t); field(t, r, "server_stopped") != false || field(t, r, "daemon_stopped") != false {
		t.Fatalf("down with nothing running: %v", r)
	}
	expectLines(t, e.run("up"), "Started local Aboard at http://127.0.0.1:"+e.port())
}
