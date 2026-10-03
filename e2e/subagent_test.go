//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// preTool runs Claude Code's pre-tool hook in the session with a Bash call's input, as
// Claude Code does before a command runs, with agentID set when the call is a
// subagent's.
func (s *session) preTool(agentID, command string) result {
	s.e.t.Helper()
	extra := `"tool_name":"Bash","tool_use_id":"toolu_1","tool_input":{"command":` + jsonString(command) +
		`,"description":"Check the board","timeout":120000,"run_in_background":false}`
	if agentID != "" {
		extra += `,"agent_id":` + jsonString(agentID) + `,"agent_type":"general-purpose"`
	}
	r := s.e.exec(s.vars, hookInput(s.id, "PreToolUse", extra), "hook", "claude-code", "pre-tool")
	if r.code != 0 {
		s.e.t.Fatalf("the pre-tool hook must always exit 0\n%s", r)
	}
	return r
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// shell runs a command line through sh in the session, as Claude Code's Bash tool does,
// with this test's aboard first on the PATH.
func (s *session) shell(line string) result {
	s.e.t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = s.e.dir
	cmd.Env = append(append(slices.Clone(s.e.vars), s.vars...), "PATH="+filepath.Dir(s.e.bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{args: []string{"sh", "-c", line}, stdout: stdout.String(), stderr: stderr.String()}
	if exit, ok := err.(*exec.ExitError); ok {
		r.code = exit.ExitCode()
	} else if err != nil {
		s.e.t.Fatal(err)
	}
	return r
}

// Inside a subagent, Claude Code's pre-tool hook marks an aboard command as the
// subagent's: it prefixes the command with export ABOARD_SUBAGENT=<agent_id>, keeps the
// tool's other input, and gives no permission decision. The marked command then refuses
// to post as the parent. Other commands, and every command of the main conversation,
// get no output, so they run as they are.
func TestClaudeSubagentAboardCommandsAreMarked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claude := e.claudeSession("5f1c2d3e-0000-4000-8000-0000000000b1")
	claude.run("pair", "writer-reviewer", "--name", "writer")

	r := claude.preTool("a1b2c3d4e5", "cd /tmp && aboard say --to @reviewer 'from the subagent'")
	var out struct {
		HookSpecificOutput map[string]json.RawMessage `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%s", err, r)
	}
	if _, ok := out.HookSpecificOutput["permissionDecision"]; ok {
		t.Fatalf("the hook gave a permission decision; Claude Code's own rules must decide\n%s", r)
	}
	var event string
	_ = json.Unmarshal(out.HookSpecificOutput["hookEventName"], &event)
	var input map[string]any
	_ = json.Unmarshal(out.HookSpecificOutput["updatedInput"], &input)
	want := map[string]any{
		"command":           "export ABOARD_SUBAGENT=a1b2c3d4e5; cd /tmp && aboard say --to @reviewer 'from the subagent'",
		"description":       "Check the board",
		"timeout":           float64(120000),
		"run_in_background": false,
	}
	if event != "PreToolUse" || len(input) != len(want) {
		t.Fatalf("hook output: event %q, input %v\n%s", event, input, r)
	}
	for k, v := range want {
		if input[k] != v {
			t.Fatalf("updatedInput.%s = %v, want %v\n%s", k, input[k], v, r)
		}
	}

	for _, tt := range []struct{ name, agent, command string }{
		{"a subagent's other command", "a1b2c3d4e5", "ls -la"},
		{"a word that only contains aboard", "a1b2c3d4e5", "cat skateboard.txt"},
		{"the main conversation's aboard command", "", "aboard say hello"},
	} {
		if r := claude.preTool(tt.agent, tt.command); r.stdout != "" || r.stderr != "" {
			t.Fatalf("%s: the hook printed something\n%s", tt.name, r)
		}
	}

	// The marked command, run the way Claude Code's Bash tool runs it, posts nothing.
	ran := claude.shell(input["command"].(string) + " --json")
	if ran.code != 1 || !strings.Contains(ran.stdout, `"subagent_without_seat"`) {
		t.Fatalf("the marked command wasn't refused:\n%s", ran)
	}
	if r := claude.run("read", "--json"); strings.Contains(r.stdout, "from the subagent") {
		t.Fatalf("the subagent posted as its parent:\n%s", r)
	}
}

// A subagent without a seat may read its parent's board, but every command that would
// change the parent's state is refused, whoever --as names, and nothing reaches the
// board.
func TestSubagentMayOnlyRead(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.run("say", "--to", "@writer", "For the writer.")
	sub := &session{e: e, harness: "claude-code", id: writer.id, vars: append(slices.Clone(writer.vars), "ABOARD_SUBAGENT=a1b2c3d4e5")}

	for _, args := range [][]string{
		{"read"}, {"status"}, {"inbox", "--peek"}, {"doctor"}, {"audit", "verify"}, {"version"}, {"help", "say"}, {"say", "--help"},
	} {
		if r := sub.runExit(append(args, "--json")...); r.code != 0 && r.code != 3 {
			t.Fatalf("a subagent couldn't read with aboard %s:\n%s", strings.Join(args, " "), r)
		}
	}
	if got := field(t, sub.run("status", "--json").json(t), "agent"); got != "writer" {
		t.Fatalf("a subagent's status shows %v, want its parent's agent writer", got)
	}

	line := field(t, e.run("invite", "--json").json(t), "join_line")
	for _, args := range [][]string{
		{"say", "Posted by the subagent."},
		{"say", "--as", "writer", "Posted by the subagent."},
		{"inbox"},
		{"join", line.(string)},
		{"pair", "writer-reviewer", "--new"},
		{"resume", "writer"},
		{"delivery", "off"},
		{"up"},
	} {
		r := sub.runExit(append(args, "--json")...)
		if r.code != 1 || field(t, r.json(t), "error.code") != "subagent_without_seat" {
			t.Fatalf("aboard %s in a subagent wasn't refused:\n%s", strings.Join(args, " "), r)
		}
		hint, _ := field(t, r.json(t), "error.hint").(string)
		if !strings.Contains(hint, "aboard inbox --peek") {
			t.Fatalf("the hint doesn't say what a subagent can run:\n%s", r)
		}
	}
	text := sub.runExit("say", "hi")
	if !strings.Contains(text.stderr, "runs in a subagent of a Claude Code session") {
		t.Fatalf("the refusal doesn't name the harness:\n%s", text)
	}
	if n := writer.unread(); n != 1 {
		t.Fatalf("the parent's inbox: %d unread, want the 1 message still there", n)
	}
	if r := writer.run("read", "--json"); strings.Contains(r.stdout, "Posted by the subagent.") {
		t.Fatalf("the subagent posted:\n%s", r)
	}
}

// A Codex session started from inside a Claude Code session inherits its ABOARD_SESSION,
// and CODEX_THREAD_ID, which Codex sets for every command, wins: the command acts as
// the Codex thread's agent, not the Claude Code session's.
func TestCodexStartedInsideClaudeCodeIsTakenForCodex(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claude := e.claudeSession("5f1c2d3e-0000-4000-8000-0000000000b2")
	line := field(t, claude.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)

	codex := e.codexSession("019a0000-0000-7000-8000-0000000000b2")
	codex.vars = append(append(slices.Clone(claude.vars), "CLAUDECODE=1"), codex.vars...)
	if r := codex.runExit("say", "hello", "--json"); r.code == 0 || field(t, r.json(t), "error.code") != "agent_not_selected" {
		t.Fatalf("an unbound Codex thread inside Claude Code acted as some agent:\n%s", r)
	}
	codex.run("join", line, "--name", "reviewer")
	if got := field(t, codex.run("status", "--json").json(t), "agent"); got != "reviewer" {
		t.Fatalf("inside Codex the agent is %v, want reviewer", got)
	}
	if got := field(t, claude.run("status", "--json").json(t), "agent"); got != "writer" {
		t.Fatalf("Claude Code's session is now %v, want writer", got)
	}
}

// A Claude Code started from a Codex command inherits CODEX_THREAD_ID; its session-start
// hook unsets it in the session's environment file, so its commands are its own.
func TestClaudeCodeStartedInsideCodexDropsTheThreadID(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	envFile := filepath.Join(e.home, "claude-env-inside-codex")
	r := e.exec([]string{"CLAUDE_ENV_FILE=" + envFile, "CODEX_THREAD_ID=019a0000-0000-7000-8000-0000000000b3", "OMPCODE=1"},
		hookInput("5f1c2d3e-0000-4000-8000-0000000000b3", "SessionStart", `"source":"startup"`), "hook", "claude-code", "session-start")
	if r.code != 0 {
		t.Fatalf("session-start hook failed\n%s", r)
	}
	raw, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\nunset CODEX_THREAD_ID\n") || strings.Contains(string(raw), "OMPCODE") {
		t.Fatalf("environment file:\n%s", raw)
	}
	// Sourced the way Claude Code loads it, the file leaves the command in Claude Code's session.
	s := &session{e: e, harness: "claude-code", id: "5f1c2d3e-0000-4000-8000-0000000000b3", vars: []string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-0000000000b3"}}
	s.shell(". " + envFile + " && aboard pair writer-reviewer --name writer")
	got := s.shell(". " + envFile + " && aboard status --json")
	if !strings.Contains(got.stdout, `"agent": "writer"`) {
		t.Fatalf("status inside the nested Claude Code:\n%s", got)
	}
}

// Codex runs its hooks inside a sub-agent with the root's session_id and the sub-agent's
// agent_id. Every one of them takes nothing and changes nothing: a sub-agent's turn ending
// doesn't end the root's turn, so the owner's message still reaches the root at its next
// tool call rather than going to Codex's queue.
func TestCodexSubAgentHooksTakeNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	codex := e.codexSession("019a0000-0000-7000-8000-0000000000c1")
	codex.run("join", line, "--name", "reviewer")
	codex.hook("prompt", `"prompt":"long task"`)
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: for the root thread")

	child := `"agent_id":"019a0000-0000-7000-8000-0000000000c2","agent_type":"default"`
	for _, event := range []string{"prompt", "tool", "stop", "end"} {
		if r := codex.hook(event, child); r.code != 0 || r.stdout != "" || r.stderr != "" {
			t.Fatalf("the sub-agent's %s hook did something:\n%s", event, r)
		}
	}
	eventually(t, 5*time.Second, "the root's tool hook to get the owner's message", func() bool {
		return strings.Contains(codex.tool(t, "").text, "owner: for the root thread")
	})
	for _, c := range e.fakeCodexCalls() {
		if strings.Contains(c["message"], "owner: for the root thread") {
			t.Fatalf("the sub-agent's stop hook ended the root's turn: the owner's message went to the queue")
		}
	}
}

// A Codex sub-agent's commands carry its own thread id in CODEX_THREAD_ID and the root's
// in CODEX_SESSION_ID. It may read, but not act as any agent, even one ABOARD_AGENT or
// --as names; the root conversation, whose two ids are equal, acts as usual.
func TestCodexSubAgentMayOnlyRead(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("s-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	root := "019a0000-0000-7000-8000-0000000000c3"
	codex := e.codexSession(root)
	codex.vars = append(codex.vars, "CODEX_SESSION_ID="+root)
	codex.run("join", line, "--name", "reviewer")

	sub := &session{
		e: e, harness: "codex", id: "019a0000-0000-7000-8000-0000000000c4",
		vars: []string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-0000000000c4", "CODEX_SESSION_ID=" + root, "ABOARD_AGENT=reviewer"},
	}
	sub.run("read", "--json")
	sub.run("inbox", "--peek", "--json")
	for _, args := range [][]string{{"say", "from the sub-agent"}, {"say", "--as", "reviewer", "from the sub-agent"}, {"inbox"}, {"resume", "reviewer"}} {
		r := sub.runExit(append(args, "--json")...)
		if r.code != 1 || field(t, r.json(t), "error.code") != "subagent_without_seat" {
			t.Fatalf("aboard %s in a Codex sub-agent wasn't refused:\n%s", strings.Join(args, " "), r)
		}
	}
	if r := sub.runExit("say", "hi"); !strings.Contains(r.stderr, "subagent of a Codex session") {
		t.Fatalf("the refusal doesn't name Codex:\n%s", r)
	}
	codex.run("say", "from the root thread")
}
