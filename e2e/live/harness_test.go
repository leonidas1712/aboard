//go:build live

package live

import (
	"bufio"
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

// claudeSetup is how the suite runs Claude Code on this machine: always with a scratch
// config directory (CLAUDE_CONFIG_DIR), logged in through CLAUDE_CODE_OAUTH_TOKEN, so the
// person's own ~/.claude is never read or written.
type claudeSetup struct {
	// skip says why Claude Code can't run here: it isn't installed.
	skip string
	// fail says why the suite can't run Claude Code without touching the person's own
	// config: there is no token, or it doesn't log Claude Code in.
	fail string
}

var (
	claudeOnce  sync.Once
	claudeSetUp claudeSetup
)

// requireClaude skips the test unless Claude Code is installed, and fails it unless
// CLAUDE_CODE_OAUTH_TOKEN logs Claude Code in with a scratch config directory.
func requireClaude(t *testing.T) {
	t.Helper()
	claudeOnce.Do(func() { claudeSetUp = detectClaude() })
	if claudeSetUp.skip != "" {
		t.Skip(claudeSetUp.skip)
	}
	if claudeSetUp.fail != "" {
		t.Fatal(claudeSetUp.fail)
	}
}

// detectClaude checks Claude Code is installed and that CLAUDE_CODE_OAUTH_TOKEN logs it
// in with a scratch config directory. The suite never falls back to the person's own
// config: that reads and writes their ~/.claude.json, and their own hooks would run in
// the test sessions too.
func detectClaude() claudeSetup {
	if _, err := exec.LookPath("claude"); err != nil {
		return claudeSetup{skip: "Claude Code is not installed: no claude on the PATH"}
	}
	const fix = "Run claude setup-token once and export the token it prints as CLAUDE_CODE_OAUTH_TOKEN, " +
		"so the suite can run Claude Code with a scratch config directory; it never uses your own ~/.claude."
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") == "" {
		return claudeSetup{fail: "CLAUDE_CODE_OAUTH_TOKEN is not set. " + fix}
	}
	dir, err := os.MkdirTemp("", "aboard-live-claude-")
	if err != nil {
		return claudeSetup{fail: "create a scratch config directory: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	env := append(cleanEnv(), "PATH="+os.Getenv("PATH"), "CLAUDE_CONFIG_DIR="+dir)
	if !claudeLoggedIn(env) {
		return claudeSetup{fail: "CLAUDE_CODE_OAUTH_TOKEN doesn't log Claude Code in: claude auth status says so. " + fix}
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
// ABOARD_HOME it was started with, and so do its hooks.
func (l *lab) startClaude(name, dir string) *pane {
	l.t.Helper()
	requireClaude(l.t)
	env := slices.Clone(l.vars) // newLab added CLAUDE_CONFIG_DIR
	l.trustInClaude(dir)
	p := l.start(name, dir, env, claudeArgv())
	p.waitClaudeReady()
	return p
}

// claudeArgv is how the suite runs Claude Code, followed by args (such as --resume and
// a session id).
func claudeArgv(args ...string) []string {
	// The slow task is how tests keep a turn busy; aboard itself is allowed by aboard init.
	argv := []string{"claude", "--model", claudeModel(), "--allowedTools", "Bash(./" + slowTask + ")"}
	return append(argv, args...)
}

// quit exits the harness the way a person does, with Ctrl-C twice, and waits until it
// has exited. The pane stays, so the harness can run there again.
func (p *pane) quit() {
	p.l.t.Helper()
	p.l.waitFor(30*time.Second, p.name+": the harness to exit", func() bool {
		if strings.TrimSpace(p.l.tmuxRun("display-message", "-p", "-t", p.target(), "#{pane_dead}")) == "1" {
			return true
		}
		p.keys("C-c")
		time.Sleep(300 * time.Millisecond) // the second Ctrl-C must come after the first shows its hint
		p.keys("C-c")
		return waitQuietly(3*time.Second, func() bool {
			return strings.TrimSpace(p.l.tmuxRun("display-message", "-p", "-t", p.target(), "#{pane_dead}")) == "1"
		})
	})
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

// waitClaudeReady answers Claude Code's workspace trust question, if a project the
// scratch config doesn't list as trusted shows it, and waits for its prompt. Since the
// project pre-approves aboard commands, the question's default answer is "No, exit".
// First-run setup should never show, since the scratch config is seeded past it.
func (p *pane) waitClaudeReady() {
	p.l.t.Helper()
	p.l.waitFor(90*time.Second, p.name+": Claude Code to show its prompt", func() bool {
		s := p.screen()
		switch {
		case strings.Contains(s, "Select login method"):
			p.l.t.Fatalf("%s: Claude Code asks to log in. Export a valid CLAUDE_CODE_OAUTH_TOKEN from claude setup-token.\n%s", p.name, s)
		case strings.Contains(s, "Yes, I trust this folder"):
			if selectedLine(s, "No, exit") {
				p.keys("Down")
			}
			p.keys("Enter")
			waitQuietly(5*time.Second, func() bool { return !strings.Contains(p.screen(), "Yes, I trust this folder") })
		case strings.Contains(s, "Choose the text style"):
			p.l.t.Fatalf("%s: Claude Code shows its first-run setup, which the scratch config should have skipped.\n%s", p.name, s)
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

// submit types a prompt into the harness and waits until the prompt box has taken it.
func (p *pane) submit(text string) {
	p.l.t.Helper()
	p.typeInto(text)
	prefix := text
	if len(prefix) > 30 {
		prefix = prefix[:30]
	}
	taken := func() bool { return p.d.taken(p, prefix) }
	if !waitQuietly(5*time.Second, taken) {
		// The first Enter was taken as part of the typed text, or Codex was still starting.
		p.keys("Enter")
		p.l.waitFor(10*time.Second, p.name+": the harness to take the prompt", taken)
	}
}

// codexInput is the text in Codex's prompt box: from its last line starting with › to the
// status line under it.
func codexInput(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "›") {
			return strings.Join(lines[i:], "\n")
		}
	}
	return ""
}

// idle reports whether the harness shows its prompt with no turn running.
func (p *pane) idle() bool { return p.d.idle(p) }

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
const bindPrompt = "Run the command `aboard resume %s` now. After that, whenever an Aboard message arrives, do exactly " +
	"what it asks, including any command it names, and nothing more, then stop. Once the command has run, reply only OK."

// bind makes the session act as agent, and waits for that turn to end.
func (p *pane) bind(agent string) {
	p.l.t.Helper()
	p.submit(fmt.Sprintf(bindPrompt, agent))
	p.waitIdle(3 * time.Minute)
	p.d.afterBind(p)
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

// presence is agent's presence as aboard status shows it, or "" if it can't be read.
func (l *lab) presence(agent string) string {
	r := l.exec(l.t.Context(), l.human, "status", "--as", agent, "--json")
	var out struct {
		Presence string `json:"presence"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil {
		return ""
	}
	return out.Presence
}

// waitPresence waits until agent's presence is want.
func (l *lab) waitPresence(agent, want string, timeout time.Duration) {
	l.t.Helper()
	l.waitFor(timeout, agent+" to be "+want, func() bool { return l.presence(agent) == want })
}

// watchPresence polls agent's presence in the background until the returned function
// is called, which returns each presence seen, in order, without repeats.
func (l *lab) watchPresence(agent string) func() []string {
	stop, done := make(chan struct{}), make(chan []string)
	go func() {
		var seen []string
		for {
			if p := l.presence(agent); p != "" && (len(seen) == 0 || seen[len(seen)-1] != p) {
				seen = append(seen, p)
			}
			select {
			case <-stop:
				done <- seen
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}()
	return func() []string {
		close(stop)
		return <-done
	}
}

// waitHanded waits up to 30 seconds for the first bundle the daemon hands over at or
// after since.
func (l *lab) waitHanded(since time.Time) handover {
	l.t.Helper()
	var h handover
	l.waitFor(30*time.Second, "the daemon to hand a bundle to a session", func() bool {
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
	env := slices.Clone(l.vars) // newLab added CODEX_HOME
	l.scopeCodexHooks(dir, env)
	l.trustCodexHooks(dir, home, env)
	p := l.start(name, dir, env, l.codexArgv())
	p.waitCodexReady()
	return p
}

// codexArgv is how the suite runs Codex, followed by args (such as resume and a thread
// id).
func (l *lab) codexArgv(args ...string) []string {
	return append([]string{
		"codex", "-m", codexModel(), "-s", "workspace-write",
		"--add-dir", filepath.Join(l.dir, "state"), "--add-dir", fmt.Sprintf("/tmp/aboard-%d", os.Getuid()),
		"-a", "on-request",
	}, args...)
}

// waitCodexReady answers Codex's trust questions and waits for its prompt.
func (p *pane) waitCodexReady() {
	p.l.t.Helper()
	name := p.name
	p.l.waitFor(90*time.Second, name+": Codex to show its prompt", func() bool {
		s := p.screen()
		switch {
		case strings.Contains(s, "trust") && strings.Contains(s, "1. Yes"):
			p.keys("1")
			p.keys("Enter")
			return false
		case strings.Contains(s, "Update available") && strings.Contains(s, "esc skip"):
			// Codex offers to update itself when it starts again; the suite never updates it.
			p.keys("2") // Skip
			p.keys("Enter")
			waitQuietly(5*time.Second, func() bool { return !strings.Contains(p.screen(), "Update available") })
			return false
		case strings.Contains(s, "Hooks need review") && strings.Contains(s, "Trust all and continue"):
			// trustCodexHooks should have made this unnecessary. Trusting records the hooks in
			// the test's own CODEX_HOME, never the person's.
			p.l.t.Logf("%s: Codex asks to trust hooks that trustCodexHooks trusted; trusting them in the pane", name)
			p.keys("2")
			p.keys("Enter")
			waitQuietly(5*time.Second, func() bool { return !strings.Contains(p.screen(), "Hooks need review") })
			return false
		}
		// Codex shows its prompt box while it is still loading a resumed session.
		return p.idle() && !strings.Contains(s, "Resuming session")
	})
}

// scopeCodexHooks puts the lab's variables into each hook command in the project's
// .codex/hooks.json. Codex doesn't pass the environment it was started with to its hooks,
// so without them a hook would use the person's own Aboard state. Each hook also writes
// its event to codexHookLog, so tests can see the hooks run.
func (l *lab) scopeCodexHooks(dir string, env []string) {
	l.t.Helper()
	path := filepath.Join(dir, ".codex", "hooks.json")
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		l.t.Fatal(err)
	}
	words := []string{"env"}
	for _, kv := range env {
		for _, name := range []string{"PATH=", "ABOARD_HOME=", "ABOARD_LOCAL_ADDR=", "CODEX_HOME="} {
			if strings.HasPrefix(kv, name) {
				words = append(words, shellQuote(kv))
			}
		}
	}
	// "<bin> hook codex <event>" becomes
	// env <vars> sh -c 'echo "$1" >> <log>; exec "$0" hook codex "$1"' <bin> <event>
	script := fmt.Sprintf(`echo "$1" >> %s; exec "$0" hook codex "$1"`, l.codexHookLog())
	words = append(words, "sh", "-c", "'"+script+"'", l.bin)
	quoted, err := json.Marshal(strings.Join(words, " ") + " ")
	if err != nil {
		l.t.Fatal(err)
	}
	plain, err := json.Marshal(l.bin + " hook codex ")
	if err != nil {
		l.t.Fatal(err)
	}
	// Both are JSON strings; drop the quotes to replace inside the command strings.
	from, to := string(plain[1:len(plain)-1]), string(quoted[1:len(quoted)-1])
	if !strings.Contains(string(raw), from) {
		l.t.Fatalf("%s has no hook running %s:\n%s", path, l.bin, raw)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), from, to)), 0o600); err != nil { //nolint:gosec // the lab's own project
		l.t.Fatal(err)
	}
}

// codexHookLog is where the lab's Codex hooks write each event they run, one per line.
func (l *lab) codexHookLog() string { return filepath.Join(l.dir, "codex-hooks.log") }

// codexHooksRan reports whether each event's hook has run at least once.
func (l *lab) codexHooksRan(events ...string) bool {
	raw, _ := os.ReadFile(l.codexHookLog())
	ran := strings.Fields(string(raw))
	for _, e := range events {
		if !slices.Contains(ran, e) {
			return false
		}
	}
	return true
}

// trustCodexHooks trusts the project's hooks in the test's CODEX_HOME before Codex
// starts, the way Codex's /hooks does: one [hooks.state."<key>"] entry per hook, with the
// hash Codex's app server reports for it (hooks/list). Without it, Codex 0.159 shows a
// "Hooks need review" dialog once the first prompt is typed, and runs no hook until it is
// answered.
func (l *lab) trustCodexHooks(dir, home string, env []string) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(l.t.Context(), 60*time.Second)
	defer cancel()
	cmd := command(ctx, "codex", "app-server")
	cmd.Dir, cmd.Env = dir, env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		l.t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		l.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		l.t.Fatalf("start codex app-server: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()
	enc := json.NewEncoder(stdin)
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 64<<10), 16<<20)
	call := func(id int, method string, params any) json.RawMessage {
		if err := enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			l.t.Fatalf("codex app-server %s: %v", method, err)
		}
		for lines.Scan() {
			var m struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil || *m.ID != id || m.Method != "" {
				continue // notifications and requests from the server
			}
			if len(m.Error) > 0 {
				l.t.Fatalf("codex app-server %s: %s", method, m.Error)
			}
			return m.Result
		}
		l.t.Fatalf("codex app-server ended before answering %s", method)
		return nil
	}
	call(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "aboard-live", "title": "Aboard live suite", "version": "0"}})
	if err := enc.Encode(map[string]string{"method": "initialized"}); err != nil {
		l.t.Fatal(err)
	}
	var list struct {
		Data []struct {
			Hooks []struct {
				Key         string `json:"key"`
				Source      string `json:"source"`
				CurrentHash string `json:"currentHash"`
			} `json:"hooks"`
		} `json:"data"`
	}
	raw := call(2, "hooks/list", map[string]any{"cwds": []string{dir}})
	if err := json.Unmarshal(raw, &list); err != nil {
		l.t.Fatalf("codex app-server hooks/list: %v\n%s", err, raw)
	}
	var cfg strings.Builder
	for _, d := range list.Data {
		for _, h := range d.Hooks {
			if h.Source == "project" && h.CurrentHash != "" {
				fmt.Fprintf(&cfg, "[hooks.state.%q]\ntrusted_hash = %q\n", h.Key, h.CurrentHash)
			}
		}
	}
	if cfg.Len() == 0 {
		l.t.Fatalf("codex app-server lists no project hooks for %s:\n%s", dir, raw)
	}
	appendFile(l.t, filepath.Join(home, "config.toml"), cfg.String())
}

// codexHome is the test's CODEX_HOME, which newLab adds to every command the lab runs,
// set up for Codex on first use. The delivery daemon runs codex queue with it.
func (l *lab) codexHome(setup codexSetup) string {
	l.t.Helper()
	home := filepath.Join(l.dir, "codex-home")
	if l.codexReady {
		return home
	}
	l.codexReady = true
	if l.started > 0 {
		// The first harness's hooks have started the daemon by now, without CODEX_HOME,
		// and its codex app-server would look for threads in the person's own ~/.codex.
		l.t.Fatal("set up Codex (l.codexHome) before starting any harness in a test that uses Codex")
	}
	if setup.auth != "" {
		if err := os.Symlink(setup.auth, filepath.Join(home, "auth.json")); err != nil {
			l.t.Fatal(err)
		}
	}
	// Codex never offers to update itself here: the offer can appear in the middle of a
	// test, where a typed Enter would run npm install -g on the person's own Codex. The
	// key comes first, since TOML puts keys after a table inside that table.
	appendFile(l.t, filepath.Join(home, "config.toml"), "check_for_update_on_startup = false\n")
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
