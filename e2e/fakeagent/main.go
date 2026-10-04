// Command fakeagent plays Claude Code, Codex or omp, by the name it runs as, for the
// tests of aboard swarm up: a launcher starts it as the harness profile says, and it
// does what that harness does with Aboard, with no model.
//
//   - claude: runs Aboard's session-start hook with its environment (and so its launch
//     ticket) and a CLAUDE_ENV_FILE, and its end hook when it stops. With --print it runs
//     one headless turn instead and prints {"session_id", "result"}.
//   - codex: starts its session at its first turn: the session-start hook runs without
//     Codex's environment, and the prompt's commands run with CODEX_THREAD_ID. Every
//     other codex command (queue, app-server, --version) is the fake codex at FAKE_CODEX.
//   - omp: connects to the delivery daemon as Aboard's extension does, handing in its
//     launch ticket in its hello, and gives its commands ABOARD_SESSION.
//
// A session id comes from --resume (claude, omp) or "resume" (codex), else is new. The
// first prompt is the last argument when it isn't an option's value. A prompt that says
// "Run aboard status" runs it, and each line "run: <command>" runs that command with sh,
// with the session's environment. Every start is logged as one JSON line to
// FAKE_AGENT_LOG. An interactive session then runs until it is hung up or killed.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	args := os.Args[1:]
	if name == "codex" && (len(args) == 0 || args[0] == "queue" || args[0] == "app-server" || args[0] == "--version" || args[0] == "login") {
		fake := os.Getenv("FAKE_CODEX")
		if err := syscall.Exec(fake, append([]string{"codex"}, args...), os.Environ()); err != nil { //nolint:gosec // the test's fake codex
			fail("exec the fake codex %q: %v", fake, err)
		}
	}
	if slices.Contains(args, "--version") {
		fmt.Println(map[string]string{"claude": "2.1.288 (Claude Code)", "omp": "omp/18.5.1"}[name])
		return
	}
	s := parse(name, args)
	logStart(s)
	switch name {
	case "claude":
		if s.print {
			headlessTurn(s)
			return
		}
		claude(s)
	case "codex":
		codex(s)
	case "omp":
		omp(s)
	default:
		fail("fakeagent doesn't play %q", name)
	}
}

type session struct {
	harness, id, source, prompt string
	print                       bool
	argv                        []string
	env                         []string
}

// options that take a value, so their value isn't the prompt.
var valued = map[string]bool{"--model": true, "-m": true, "--resume": true, "-r": true, "--output-format": true, "-s": true, "-a": true}

func parse(name string, args []string) *session {
	s := &session{harness: name, source: "startup", argv: args, env: os.Environ()}
	if name == "claude" {
		s.harness = "claude-code"
	}
	var free []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--print" || a == "-p":
			s.print = true
		case a == "--resume" || a == "-r":
			if i+1 < len(args) {
				s.id, s.source = args[i+1], "resume"
			}
			i++
		case valued[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			free = append(free, a)
		}
	}
	if name == "codex" && len(free) > 0 && free[0] == "resume" {
		free = free[1:]
		if len(free) > 0 {
			s.id, s.source, free = free[0], "resume", free[1:]
		}
	}
	if len(free) > 0 {
		s.prompt = free[len(free)-1]
	}
	if s.id == "" {
		s.id = newID()
	}
	return s
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func logStart(s *session) {
	path := os.Getenv("FAKE_AGENT_LOG")
	if path == "" {
		return
	}
	cwd, _ := os.Getwd()
	line, _ := json.Marshal(map[string]any{
		"harness": s.harness, "argv": s.argv, "session": s.id, "source": s.source, "prompt": s.prompt, "print": s.print,
		"agent": os.Getenv("ABOARD_AGENT"), "launch": os.Getenv("ABOARD_LAUNCH"), "headless": os.Getenv("ABOARD_HEADLESS"),
		"cwd": cwd, "pid": os.Getpid(),
	})
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // the log the test named
	if err != nil {
		fail("open FAKE_AGENT_LOG: %v", err)
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// hook runs one of Aboard's hooks as the harness does, with env as its environment.
func hook(s *session, run string, env []string, input map[string]any) {
	input["session_id"] = s.id
	raw, _ := json.Marshal(input)
	cmd := exec.CommandContext(context.Background(), "aboard", "hook", s.harness, run) //nolint:gosec // the harness's own name
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, strings.NewReader(string(raw)), os.Stderr, os.Stderr
	_ = cmd.Run()
}

func claude(s *session) {
	envFile := filepath.Join(os.TempDir(), "fakeagent-env-"+s.id)
	_ = os.Remove(envFile) //nolint:gosec // a file of this process's own
	hook(s, "session-start", append(slices.Clone(s.env), "CLAUDE_ENV_FILE="+envFile), map[string]any{"hook_event_name": "SessionStart", "source": s.source})
	raw, _ := os.ReadFile(filepath.Clean(envFile)) //nolint:gosec // a file of this process's own
	for _, line := range strings.Split(string(raw), "\n") {
		if kv, ok := strings.CutPrefix(strings.TrimSpace(line), "export "); ok {
			s.env = append(s.env, kv)
		}
	}
	_ = os.Remove(envFile) //nolint:gosec // a file of this process's own
	runPrompt(s)
	waitUntilStopped()
	hook(s, "end", s.env, map[string]any{"hook_event_name": "SessionEnd"})
}

func codex(s *session) {
	if s.prompt != "" {
		// Codex runs its hooks in its own app server, which doesn't have the environment
		// Codex was started with.
		hookEnv := slices.DeleteFunc(slices.Clone(s.env), func(kv string) bool {
			return strings.HasPrefix(kv, "ABOARD_LAUNCH=") || strings.HasPrefix(kv, "ABOARD_AGENT=")
		})
		hook(s, "session-start", hookEnv, map[string]any{"hook_event_name": "SessionStart", "source": s.source})
		s.env = append(s.env, "CODEX_THREAD_ID="+s.id, "CODEX_SESSION_ID="+s.id)
		runPrompt(s)
	}
	waitUntilStopped()
}

func omp(s *session) {
	conn := connect()
	hello, _ := json.Marshal(map[string]any{
		"v": 1, "op": "hello", "harness": "omp", "session": s.id, "boot": newID()[:16], "source": s.source,
		"process": map[string]any{"pid": os.Getpid()}, "extension_version": "fake", "launch": os.Getenv("ABOARD_LAUNCH"),
	})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		fail("send hello: %v", err)
	}
	welcome, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || !strings.Contains(welcome, `"welcome"`) {
		fail("no welcome from the daemon: %q %v", welcome, err)
	}
	s.env = append(s.env, "ABOARD_SESSION=omp:"+s.id)
	runPrompt(s)
	waitUntilStopped()
	bye, _ := json.Marshal(map[string]any{"v": 1, "op": "goodbye"})
	_, _ = conn.Write(append(bye, '\n'))
	_ = conn.Close()
}

// connect reaches the delivery daemon's socket (spec/control.md, Transport), starting
// the daemon as the extension does when it isn't there.
func connect() net.Conn {
	home := os.Getenv("ABOARD_HOME")
	if home == "" {
		fail("fakeagent's omp needs ABOARD_HOME")
	}
	state := filepath.Join(home, "state")
	path := filepath.Join(state, "daemon.sock")
	if len(path) > 100 {
		sum := sha256.Sum256([]byte(state))
		path = filepath.Join("/tmp/aboard-"+strconv.Itoa(os.Getuid()), hex.EncodeToString(sum[:8])+".sock")
	}
	var d net.Dialer
	if conn, err := d.DialContext(context.Background(), "unix", path); err == nil { //nolint:gosec // the daemon's socket
		return conn
	}
	start := exec.CommandContext(context.Background(), "aboard", "daemon", "start")
	start.Stdout, start.Stderr = os.Stderr, os.Stderr
	_ = start.Run()
	conn, err := d.DialContext(context.Background(), "unix", path) //nolint:gosec // the daemon's socket
	if err != nil {
		fail("reach the daemon at %s: %v", path, err)
	}
	return conn
}

// headlessTurn plays claude --print: it runs what the prompt asks and prints the result.
func headlessTurn(s *session) {
	runPrompt(s)
	out, _ := json.Marshal(map[string]any{"type": "result", "session_id": s.id, "result": "done"})
	fmt.Println(string(out))
}

// runPrompt runs what the prompt asks for, with the session's environment.
func runPrompt(s *session) {
	var commands []string
	if strings.Contains(s.prompt, "Run aboard status") {
		commands = append(commands, "aboard status")
	}
	for _, line := range strings.Split(s.prompt, "\n") {
		if c, ok := strings.CutPrefix(strings.TrimSpace(line), "run: "); ok {
			commands = append(commands, c)
		}
	}
	for _, c := range commands {
		cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", c) //nolint:gosec // what the test's prompt asks to run
		cmd.Env, cmd.Stdout, cmd.Stderr = s.env, os.Stderr, os.Stderr
		_ = cmd.Run()
	}
}

// waitUntilStopped waits for a hang-up or a kill, or until the test that started the
// session (ABOARD_EXIT_WITH_PID) is gone.
func waitUntilStopped() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	owner, _ := strconv.Atoi(os.Getenv("ABOARD_EXIT_WITH_PID"))
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if owner > 0 && syscall.Kill(owner, 0) == syscall.ESRCH {
				return
			}
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fakeagent: "+format+"\n", args...)
	os.Exit(2)
}
