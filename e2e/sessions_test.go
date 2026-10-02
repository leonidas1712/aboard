//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// session plays a harness session: it carries the environment the harness gives every
// command the agent runs, and calls the hook commands the harness would call.
type session struct {
	e       *env
	harness string // "claude-code" or "codex"
	id      string
	vars    []string
}

// claudeSession starts a Claude Code session the way Claude Code does: it runs the
// session-start hook with the session id on stdin and an environment file, then gives
// every later command the variables the hook wrote there.
func (e *env) claudeSession(id string) *session {
	e.t.Helper()
	envFile := filepath.Join(e.home, "claude-env-"+id)
	s := &session{e: e, harness: "claude-code", id: id}
	r := e.exec([]string{"CLAUDE_ENV_FILE=" + envFile}, hookInput(id, "SessionStart", `"source":"startup"`), "hook", "claude-code", "session-start")
	if r.code != 0 {
		e.t.Fatalf("session-start hook failed\n%s", r)
	}
	raw, err := os.ReadFile(envFile)
	if err != nil {
		e.t.Fatalf("session-start hook wrote no environment file: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "export "); ok {
			s.vars = append(s.vars, strings.Trim(v, `'"`))
		}
	}
	if !strings.Contains(string(raw), "ABOARD_SESSION=claude-code:"+id) {
		e.t.Fatalf("environment file doesn't name the session:\n%s", raw)
	}
	return s
}

// claudeSessionIn starts a Claude Code session whose session-start hook runs under a
// harness process of its own, and returns the session with that process, which the test
// can kill as a crash would.
func (e *env) claudeSessionIn(id string) (*session, *exec.Cmd) {
	e.t.Helper()
	envFile := filepath.Join(e.home, "claude-env-"+id)
	cmd := exec.Command(fakeHarness, binary, "hook", "claude-code", "session-start")
	cmd.Dir = e.dir
	cmd.Env = append(append([]string{}, e.vars...), "CLAUDE_ENV_FILE="+envFile)
	cmd.Stdin = strings.NewReader(hookInput(id, "SessionStart", `"source":"startup"`))
	out, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "exit 0" {
		e.t.Fatalf("session-start hook under the harness: %q %v\n%s", line, err, stderr.String())
	}
	s := &session{e: e, harness: "claude-code", id: id}
	raw, err := os.ReadFile(envFile)
	if err != nil {
		e.t.Fatalf("session-start hook wrote no environment file: %v", err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "export "); ok {
			s.vars = append(s.vars, strings.Trim(v, `'"`))
		}
	}
	return s, cmd
}

// codexSession starts a Codex session: Codex sets CODEX_THREAD_ID for every command the
// agent runs, and calls the session-start hook.
func (e *env) codexSession(threadID string) *session {
	e.t.Helper()
	s := &session{e: e, harness: "codex", id: threadID, vars: []string{"CODEX_THREAD_ID=" + threadID}}
	if r := s.hook("session-start", `"source":"startup"`); r.code != 0 {
		e.t.Fatalf("codex session-start hook failed\n%s", r)
	}
	return s
}

func hookInput(id, event, extra string) string {
	in := `{"session_id":"` + id + `","hook_event_name":"` + event + `","cwd":"/tmp"`
	if extra != "" {
		in += "," + extra
	}
	return in + "}"
}

var hookEvents = map[string]string{
	"session-start": "SessionStart", "prompt": "UserPromptSubmit", "stop": "Stop",
	"tool": "PostToolUse", "end": "SessionEnd",
}

// run runs an aboard command inside the session and fails unless it exits 0.
func (s *session) run(args ...string) result {
	s.e.t.Helper()
	r := s.e.exec(s.vars, "", args...)
	if r.code != 0 {
		s.e.t.Fatalf("command failed in session %s:\n%s\nserver log:\n%s", s.id, r, s.e.serverLog())
	}
	return r
}

// runExit runs an aboard command inside the session and returns whatever happened.
func (s *session) runExit(args ...string) result {
	s.e.t.Helper()
	return s.e.exec(s.vars, "", args...)
}

// hook calls one of the session's hook commands and waits for it.
func (s *session) hook(event, extra string) result {
	s.e.t.Helper()
	return s.e.exec(s.vars, hookInput(s.id, hookEvents[event], extra), "hook", s.harness, event)
}

// proc is a command running in the background, such as a waiting stop hook.
type proc struct {
	t    *testing.T
	cmd  *exec.Cmd
	out  *bytes.Buffer
	errb *bytes.Buffer
	done chan error
}

// startHook starts a hook command in the background.
func (s *session) startHook(event string) *proc {
	s.e.t.Helper()
	cmd := exec.Command(s.e.bin, "hook", s.harness, event)
	cmd.Dir = s.e.dir
	cmd.Env = append(append([]string{}, s.e.vars...), s.vars...)
	cmd.Stdin = strings.NewReader(hookInput(s.id, hookEvents[event], ""))
	p := &proc{t: s.e.t, cmd: cmd, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = p.out, p.errb
	if err := cmd.Start(); err != nil {
		s.e.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	s.e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	return p
}

// startHookHeld starts a hook command but holds back its input, so the process is
// running but hasn't reached the daemon yet. Calling the returned function sends the
// input.
func (s *session) startHookHeld(event string) (*proc, func()) {
	s.e.t.Helper()
	cmd := exec.Command(s.e.bin, "hook", s.harness, event)
	cmd.Dir = s.e.dir
	cmd.Env = append(append([]string{}, s.e.vars...), s.vars...)
	in, err := cmd.StdinPipe()
	if err != nil {
		s.e.t.Fatal(err)
	}
	p := &proc{t: s.e.t, cmd: cmd, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = p.out, p.errb
	if err := cmd.Start(); err != nil {
		s.e.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	s.e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	release := func() {
		_, _ = io.WriteString(in, hookInput(s.id, hookEvents[event], ""))
		_ = in.Close()
	}
	return p, release
}

// wait waits for the process to exit and returns its result.
func (p *proc) wait(within time.Duration) result {
	p.t.Helper()
	select {
	case err := <-p.done:
		r := result{args: p.cmd.Args[1:], stdout: p.out.String(), stderr: p.errb.String()}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			r.code = exit.ExitCode()
		}
		return r
	case <-time.After(within):
		p.t.Fatalf("%v still running after %s\nstderr so far:\n%s", p.cmd.Args, within, p.errb.String())
		return result{}
	}
}

// running reports whether the process is still running after a short grace period.
func (p *proc) running(grace time.Duration) bool {
	select {
	case err := <-p.done:
		p.done <- err
		return false
	case <-time.After(grace):
		return true
	}
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// unread is how many messages the session's agent has not yet had acknowledged.
func (s *session) unread() int {
	s.e.t.Helper()
	r := s.run("inbox", "--peek", "--json")
	return len(field(s.e.t, r.json(s.e.t), "messages").([]any))
}

// fakeCodexCalls returns every message the fake codex binary was asked to queue.
func (e *env) fakeCodexCalls() []map[string]string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.home, "fake-codex-queue.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var calls []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c map[string]string
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			e.t.Fatalf("fake codex log line %q: %v", line, err)
		}
		calls = append(calls, c)
	}
	return calls
}
