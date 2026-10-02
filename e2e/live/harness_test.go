//go:build live

package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// claudeSetup is how the suite runs Claude Code on this machine.
type claudeSetup struct {
	// skip says why Claude Code can't run here; empty when it can.
	skip string
	// isolated is true when Claude Code keeps its config in a scratch directory
	// (CLAUDE_CONFIG_DIR) and is still logged in there. Otherwise it uses the person's
	// own config directory, which it writes its project list to.
	isolated bool
}

var (
	claudeOnce  sync.Once
	claudeSetUp claudeSetup
)

// requireClaude skips the test unless Claude Code is installed and logged in.
func requireClaude(t *testing.T) claudeSetup {
	t.Helper()
	claudeOnce.Do(func() { claudeSetUp = detectClaude() })
	if claudeSetUp.skip != "" {
		t.Skip(claudeSetUp.skip)
	}
	return claudeSetUp
}

// detectClaude checks Claude Code is installed and logged in, first with a scratch config
// directory and then with the person's own. LIVE_CLAUDE_CONFIG=home skips the first.
func detectClaude() claudeSetup {
	if _, err := exec.LookPath("claude"); err != nil {
		return claudeSetup{skip: "Claude Code is not installed: no claude on the PATH"}
	}
	env := append(cleanEnv(), "PATH="+os.Getenv("PATH"))
	if os.Getenv("LIVE_CLAUDE_CONFIG") != "home" {
		dir, err := os.MkdirTemp("", "aboard-live-claude-")
		if err == nil {
			defer func() { _ = os.RemoveAll(dir) }()
			if claudeLoggedIn(append(slices.Clone(env), "CLAUDE_CONFIG_DIR="+dir)) {
				return claudeSetup{isolated: true}
			}
		}
	}
	if !claudeLoggedIn(env) {
		return claudeSetup{skip: "Claude Code is not logged in: claude auth status says so"}
	}
	// With the person's own config, their global settings apply too. Aboard's hooks
	// there would run alongside the project's, so the results would prove nothing.
	home, _ := os.UserHomeDir()
	if raw, err := os.ReadFile(filepath.Clean(filepath.Join(home, ".claude", "settings.json"))); err == nil &&
		strings.Contains(string(raw), " hook claude-code ") {
		return claudeSetup{skip: "~/.claude/settings.json holds Aboard's hooks, which would run in the test sessions too; " +
			"Claude Code isn't logged in with a scratch config directory, so the suite can't avoid them"}
	}
	return claudeSetup{}
}

func claudeLoggedIn(env []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := command(ctx, "claude", "auth", "status")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var st struct {
		LoggedIn bool `json:"loggedIn"`
	}
	return json.Unmarshal(out, &st) == nil && st.LoggedIn
}

// startClaude starts Claude Code in dir, the way a person starts it in a project, and
// waits until it takes a prompt. Commands it runs see the lab's Aboard state through the
// XDG variables it was started with, and so do its hooks.
func (l *lab) startClaude(name, dir string) *pane {
	l.t.Helper()
	setup := requireClaude(l.t)
	env := slices.Clone(l.vars)
	if setup.isolated {
		if l.claudeConfig == "" {
			l.claudeConfig = filepath.Join(l.dir, "claude-config")
		}
		l.trustInClaude(dir)
		env = append(env, "CLAUDE_CONFIG_DIR="+l.claudeConfig)
	}
	// The slow task is how tests keep a turn busy; aboard itself is allowed by aboard init.
	argv := []string{"claude", "--allowedTools", "Bash(./" + slowTask + ")"}
	if model := os.Getenv("LIVE_CLAUDE_MODEL"); model != "" {
		argv = append(argv, "--model", model)
	}
	p := l.start(name, dir, env, argv)
	p.waitClaudeReady()
	return p
}

// trustInClaude records, in the scratch config directory, that first-run setup is done
// and dir is trusted, so Claude Code opens straight to its prompt.
func (l *lab) trustInClaude(dir string) {
	l.t.Helper()
	if err := os.MkdirAll(l.claudeConfig, 0o700); err != nil {
		l.t.Fatal(err)
	}
	path := filepath.Join(l.claudeConfig, ".claude.json")
	cfg := map[string]any{}
	if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	} else if !errors.Is(err, fs.ErrNotExist) {
		l.t.Fatal(err)
	}
	cfg["hasCompletedOnboarding"] = true
	cfg["theme"] = "dark"
	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	paths := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		paths = append(paths, resolved)
	}
	for _, p := range paths {
		entry, _ := projects[p].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["hasTrustDialogAccepted"] = true
		projects[p] = entry
	}
	cfg["projects"] = projects
	raw, err := json.Marshal(cfg)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		l.t.Fatal(err)
	}
}

// waitClaudeReady answers Claude Code's first-run questions and waits for its prompt.
// With the person's own config, a new project shows the workspace trust question; since
// the project pre-approves aboard commands, its default answer is "No, exit".
func (p *pane) waitClaudeReady() {
	p.l.t.Helper()
	p.l.waitFor(90*time.Second, p.name+": Claude Code to show its prompt", func() bool {
		s := p.screen()
		switch {
		case strings.Contains(s, "Select login method"):
			p.l.t.Fatalf("%s: Claude Code asks to log in. Log in to Claude Code, or set LIVE_CLAUDE_CONFIG=home "+
				"if its login lives in its own config directory.\n%s", p.name, s)
		case strings.Contains(s, "Yes, I trust this folder"):
			if selectedLine(s, "No, exit") {
				p.keys("Down")
			}
			p.keys("Enter")
			waitQuietly(5*time.Second, func() bool { return !strings.Contains(p.screen(), "Yes, I trust this folder") })
		case strings.Contains(s, "Choose the text style"):
			p.keys("Enter")
		case claudeReady(s):
			return true
		}
		return false
	})
}

// selectedLine reports whether the menu's highlighted line contains text.
func selectedLine(screen, text string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if promptMarker(strings.TrimSpace(line)) != "" && strings.Contains(line, text) {
			return true
		}
	}
	return false
}

// inputLine finds Claude Code's prompt box: the last line starting with a prompt marker, between two
// horizontal rules. It returns the box's text and whether the box shows.
func inputLine(screen string) (string, bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i := len(lines) - 1; i > 0; i-- {
		line := strings.TrimSpace(lines[i])
		marker := promptMarker(line)
		if marker == "" {
			continue
		}
		if i+1 < len(lines) && isRule(lines[i-1]) && isRule(lines[i+1]) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker)), true
		}
		return "", false
	}
	return "", false
}

// promptMarker is the marker a prompt or menu line starts with: ❯, or > in a terminal
// without Unicode.
func promptMarker(line string) string {
	for _, m := range []string{"❯", ">"} {
		if strings.HasPrefix(line, m) {
			return m
		}
	}
	return ""
}

func isRule(line string) bool {
	line = strings.TrimSpace(line)
	return len(line) > 10 && strings.Trim(line, "─") == ""
}

func claudeReady(screen string) bool { _, ok := inputLine(screen); return ok }

// claudeBusy reports whether a turn runs: Claude Code shows how to interrupt it.
func claudeBusy(screen string) bool { return strings.Contains(screen, "esc to interrupt") }

// submit types a prompt into Claude Code and waits until the prompt box has taken it.
func (p *pane) submit(text string) {
	p.l.t.Helper()
	p.typeInto(text)
	prefix := text
	if len(prefix) > 30 {
		prefix = prefix[:30]
	}
	taken := func() bool {
		in, ok := inputLine(p.screen())
		return !ok || !strings.Contains(in, prefix)
	}
	if !waitQuietly(5*time.Second, taken) {
		p.keys("Enter") // the first Enter was taken as part of the typed text
		p.l.waitFor(10*time.Second, p.name+": Claude Code to take the prompt", taken)
	}
}

// idle reports whether the harness shows its prompt with no turn running.
func (p *pane) idle() bool {
	s := p.screen()
	if p.harness == "codex" {
		return strings.Contains(s, "context left") && !strings.Contains(s, "Working")
	}
	return claudeReady(s) && !claudeBusy(s)
}

// waitIdle waits until the harness has shown its prompt with no turn running for a
// second, so a Claude Code turn's stop hook has started waiting.
func (p *pane) waitIdle(timeout time.Duration) {
	p.l.t.Helper()
	var since time.Time
	p.l.waitFor(timeout, p.name+": the turn to end", func() bool {
		if !p.idle() {
			since = time.Time{}
			return false
		}
		if since.IsZero() {
			since = time.Now()
		}
		return time.Since(since) >= time.Second
	})
}

// bindPrompt is every harness session's first prompt: it takes over an agent made in
// the person's terminal and says how to treat messages, so each test message can ask
// for one checkable action.
const bindPrompt = "Run `aboard resume %s`. After that, whenever an Aboard message arrives, do exactly what it asks, " +
	"including any command it names, and nothing more, then stop. Now reply only OK."

// bind makes the session act as agent, and waits for that turn to end.
func (p *pane) bind(agent string) {
	p.l.t.Helper()
	p.submit(fmt.Sprintf(bindPrompt, agent))
	p.waitIdle(3 * time.Minute)
}

// slowTask is a script tests put in a project to keep a turn busy. Claude Code refuses
// a long sleep run on its own in the foreground, so the sleep is in a script.
const slowTask = "slow-task.sh"

// writeSlowTask puts a slow task that takes seconds into dir. While it runs, a process
// "sleep <seconds>" shows it.
func writeSlowTask(t *testing.T, dir string, seconds int) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nsleep %d\necho finished\n", seconds)
	if err := os.WriteFile(filepath.Join(dir, slowTask), []byte(script), 0o700); err != nil { //nolint:gosec // the task must be executable
		t.Fatal(err)
	}
}

// joinLine matches the line aboard pair prints for the other session.
var joinLine = regexp.MustCompile(`Join Aboard board [a-z0-9-]+ on \S+ as [a-z0-9-]+ with code [A-Za-z0-9]{3}-?[A-Za-z0-9]{3}`)

// waitMessage waits for a message from sender, posted at or after since, whose body
// contains text.
func (l *lab) waitMessage(sender string, since time.Time, text string, timeout time.Duration) message {
	l.t.Helper()
	var found message
	l.waitFor(timeout, fmt.Sprintf("%s to post %q", sender, text), func() bool {
		msgs, _, _ := l.tryMessages(sender)
		for _, m := range msgs {
			if m.From.Name == sender && !m.At.Before(since) && strings.Contains(m.Body, text) {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// waitHanded waits for the first bundle the daemon hands over at or after since.
func (l *lab) waitHanded(since time.Time, timeout time.Duration) handover {
	l.t.Helper()
	var h handover
	l.waitFor(timeout, "the daemon to hand a bundle to a session", func() bool {
		hs := l.handedAfter(since)
		if len(hs) == 0 {
			return false
		}
		h = hs[0]
		return true
	})
	return h
}

// waitQuiet waits until every pane is idle and the board and the daemon have been
// still for ten seconds: the agents have stopped talking.
func (l *lab) waitQuiet(timeout time.Duration, reader string, panes ...*pane) {
	l.t.Helper()
	last, lastCount := time.Now(), -1
	l.waitFor(timeout, "the agents to stop talking", func() bool {
		count := len(l.messages(reader)) + len(l.handed())
		for _, p := range panes {
			if !p.idle() {
				count = -2
			}
		}
		if count != lastCount {
			last, lastCount = time.Now(), count
			return false
		}
		return time.Since(last) >= 10*time.Second
	})
}

// codexSetup is how the suite runs Codex on this machine.
type codexSetup struct {
	skip string
	// auth is the person's Codex login file, linked into each test's CODEX_HOME.
	auth string
}

var (
	codexOnce  sync.Once
	codexSetUp codexSetup
)

// requireCodex skips the test unless Codex is installed, logged in and has a queue.
func requireCodex(t *testing.T) codexSetup {
	t.Helper()
	codexOnce.Do(func() { codexSetUp = detectCodex() })
	if codexSetUp.skip != "" {
		t.Skip(codexSetUp.skip)
	}
	return codexSetUp
}

func detectCodex() codexSetup {
	if _, err := exec.LookPath("codex"); err != nil {
		return codexSetup{skip: "Codex is not installed: no codex on the PATH"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env := append(cleanEnv(), "PATH="+os.Getenv("PATH"))
	login := command(ctx, "codex", "login", "status")
	login.Env = env
	if err := login.Run(); err != nil {
		return codexSetup{skip: "Codex is not logged in: codex login status fails"}
	}
	queue := command(ctx, "codex", "queue", "--help")
	queue.Env = env
	if err := queue.Run(); err != nil {
		return codexSetup{skip: "this Codex has no queue command; update Codex"}
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	auth := filepath.Join(home, "auth.json")
	if _, err := os.Stat(auth); err != nil { //nolint:gosec // the person's own CODEX_HOME
		auth = ""
	}
	return codexSetup{auth: auth}
}

// startCodex starts Codex in dir with a CODEX_HOME of the test's own, so the person's
// ~/.codex/config.toml never gets the project and hook entries Codex writes. The login
// file is linked, not copied, so a token Codex refreshes stays the person's.
func (l *lab) startCodex(name, dir string) *pane {
	l.t.Helper()
	setup := requireCodex(l.t)
	home := l.codexHome(setup)
	// Codex reads a project's .codex folder once the project is trusted; the sandbox
	// needs the network for aboard commands its rules don't cover.
	cfg := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	appendFile(l.t, filepath.Join(home, "config.toml"), cfg)
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"),
		[]byte("[sandbox_workspace_write]\nnetwork_access = true\n"), 0o600); err != nil {
		l.t.Fatal(err)
	}
	env := append(slices.Clone(l.vars), "CODEX_HOME="+home)
	argv := []string{
		"codex", "-s", "workspace-write",
		"--add-dir", filepath.Join(l.dir, "state"), "--add-dir", fmt.Sprintf("/tmp/aboard-%d", os.Getuid()),
		"-a", "on-request",
	}
	p := l.start(name, dir, env, argv)
	p.l.waitFor(90*time.Second, name+": Codex to show its prompt", func() bool {
		if s := p.screen(); strings.Contains(s, "trust") && strings.Contains(s, "1. Yes") {
			p.keys("1")
			p.keys("Enter")
			return false
		}
		return p.idle()
	})
	return p
}

// codexHome is the test's CODEX_HOME, made on first use. The delivery daemon runs codex
// queue with the same CODEX_HOME, so the lab's commands carry it too.
func (l *lab) codexHome(setup codexSetup) string {
	l.t.Helper()
	home := filepath.Join(l.dir, "codex-home")
	if _, err := os.Stat(home); err == nil {
		return home
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		l.t.Fatal(err)
	}
	if setup.auth != "" {
		if err := os.Symlink(setup.auth, filepath.Join(home, "auth.json")); err != nil {
			l.t.Fatal(err)
		}
	}
	l.vars = append(l.vars, "CODEX_HOME="+home)
	// The daemon runs codex app-server and codex queue, so it must start outside Codex's
	// sandbox, with this CODEX_HOME.
	l.run("daemon", "start")
	return home
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
