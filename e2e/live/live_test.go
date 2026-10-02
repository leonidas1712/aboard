//go:build live

// Package live proves delivery, setup and upgrades in the real harnesses: it drives
// Claude Code and Codex in tmux against a real aboard, with Aboard's state in a scratch
// directory, and decides each result from the board and aboard doctor. Every test spends
// real model turns, so it runs only with make live, never in make check.
package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// repoRoot is the repository, two levels up from this package.
const repoRoot = "../.."

// newBinary is aboard built from the source under test.
var newBinary string

// realConfig is the checksum of the person's own harness config and Aboard state,
// recorded before any test runs.
var realConfig map[string]string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	realConfig = configSums()
	dir, err := os.MkdirTemp("", "aboard-live-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	newBinary = filepath.Join(dir, "aboard")
	if err := buildAboard(context.Background(), newBinary, ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	code := m.Run()
	if diff := configDiff(realConfig, configSums()); diff != "" {
		fmt.Fprintln(os.Stderr, "live: the real harness config or Aboard state changed during the run:\n"+diff)
		return 1
	}
	return code
}

// buildAboard builds aboard to out, stamped with version when it isn't empty.
func buildAboard(ctx context.Context, out, version string) error {
	args := []string{"build", "-o", out}
	if version != "" {
		args = append(args, "-ldflags", "-X github.com/leonidas1712/aboard/server/internal/cli.version="+version)
	}
	args = append(args, "./server/cmd/aboard")
	cmd := command(ctx, "go", args...)
	cmd.Dir = repoRoot
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("build aboard: %w\n%s", err, b)
	}
	return nil
}

// command is the one place the suite starts a process; every name and argument comes
// from the test itself.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...) //nolint:gosec // the suite's own commands
}

// configSums records the person's harness config files and Aboard state directories: a
// sha256 per file, or "absent". Live runs must leave every one of them as it was.
func configSums() map[string]string {
	home, _ := os.UserHomeDir()
	sums := map[string]string{}
	for _, f := range []string{".claude/settings.json", ".codex/config.toml", ".codex/hooks.json"} {
		path := filepath.Join(home, f)
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			sums[path] = "absent"
			continue
		}
		sum := sha256.Sum256(raw)
		sums[path] = hex.EncodeToString(sum[:])
	}
	for _, d := range []string{".local/state/aboard", ".local/share/aboard", ".config/aboard"} {
		path := filepath.Join(home, d)
		if _, err := os.Stat(path); err == nil {
			sums[path] = "present"
		} else {
			sums[path] = "absent"
		}
	}
	return sums
}

// configDiff lists every path whose checksum or presence changed.
func configDiff(before, after map[string]string) string {
	var b strings.Builder
	for path, sum := range before {
		if after[path] != sum {
			fmt.Fprintf(&b, "  %s: was %s, now %s\n", path, sum, after[path])
		}
	}
	return b.String()
}

// harnessMarkers are the variables a harness sets for what runs inside it. A harness the
// suite starts must not inherit them from the session that runs the suite: they would
// make aboard think it runs inside that session, and in a remote Claude Code container
// they make the nested Claude Code take over the outer session's id.
var harnessMarkers = regexp.MustCompile(`^(CLAUDE|CODEX|ABOARD|TMUX)[A-Z0-9_]*=`)

// cleanEnv is this process's environment without harness markers, keeping what the
// harnesses need for their login (HOME, ANTHROPIC_*, OPENAI_*, CLAUDE_CONFIG_DIR, and
// CLAUDE_CODE_OAUTH_TOKEN, which logs Claude Code in with a scratch config directory).
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR="), strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN="):
		case harnessMarkers.MatchString(kv),
			strings.HasPrefix(kv, "XDG_CONFIG_HOME="), strings.HasPrefix(kv, "XDG_DATA_HOME="),
			strings.HasPrefix(kv, "XDG_STATE_HOME="), strings.HasPrefix(kv, "PATH="):
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lab is one isolated machine for a test: aboard in its own directory, Aboard's state
// under XDG directories in the scratch directory, a free port for the local server, and a
// tmux server for the harness sessions.
type lab struct {
	t *testing.T
	// dir is the scratch directory; everything the test writes is under it.
	dir string
	// bin is the aboard binary the hooks and commands run.
	bin string
	// human is the directory the person runs aboard commands in.
	human string
	addr  string
	vars  []string
	// claudeConfig is Claude Code's config directory for this test, or empty when
	// Claude Code uses the person's own (see claudeSetup).
	claudeConfig string
	tmux         string
	panes        []*pane
	// typed counts the prompts typed into harnesses, for the turn estimate.
	typed int
}

// newLab makes a lab running aboard built from the source under test.
func newLab(t *testing.T) *lab {
	t.Helper()
	return newLabWith(t, newBinary)
}

// newLabWith makes a lab whose aboard is a copy of binary, at a path of the lab's own.
func newLabWith(t *testing.T, binary string) *lab {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed; the live suite drives harnesses in tmux")
	}
	// Resolve links in the temp path (on macOS /var is a link to /private/var), so paths
	// the binary prints match the paths the test compares them with.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A socket path can't be longer than 104 bytes on macOS, and the test's temp path
	// often is, so tmux's socket lives in a short directory of its own.
	sockDir, err := os.MkdirTemp("/tmp", "abl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	l := &lab{
		t:     t,
		dir:   dir,
		bin:   filepath.Join(dir, "bin", "aboard"),
		human: filepath.Join(dir, "human"),
		addr:  freeAddr(t),
		tmux:  filepath.Join(sockDir, "tmux.sock"),
	}
	for _, d := range []string{filepath.Dir(l.bin), l.human} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, binary, l.bin)
	state := filepath.Join(dir, "state")
	l.vars = append(cleanEnv(),
		"PATH="+filepath.Dir(l.bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_CONFIG_HOME="+filepath.Join(state, "config"),
		"XDG_DATA_HOME="+filepath.Join(state, "data"),
		"XDG_STATE_HOME="+filepath.Join(state, "st"),
		"ABOARD_LOCAL_ADDR="+l.addr,
	)
	t.Cleanup(l.teardown)
	return l
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(src))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o750); err != nil { //nolint:gosec // aboard must be executable
		t.Fatal(err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func (l *lab) stateDir() string  { return filepath.Join(l.dir, "state", "st", "aboard") }
func (l *lab) dataDir() string   { return filepath.Join(l.dir, "state", "data", "aboard") }
func (l *lab) configDir() string { return filepath.Join(l.dir, "state", "config", "aboard") }

// teardown saves what explains a failure, stops every harness and aboard process the lab
// started, and checks the person's own config is untouched.
func (l *lab) teardown() {
	t := l.t
	if t.Failed() || os.Getenv("LIVE_KEEP") != "" {
		l.saveArtifacts()
	}
	ctx := context.Background()
	// A harness runs its end hook as it exits, and that can start a daemon, so wait for
	// the harnesses to be gone before stopping aboard.
	var harnesses []int
	for _, p := range l.panes {
		out, err := command(ctx, "tmux", "-S", l.tmux, "display-message", "-p", "-t", p.target(), "#{pane_pid}").Output()
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && convErr == nil {
			harnesses = append(harnesses, pid)
		}
	}
	_ = command(ctx, "tmux", "-S", l.tmux, "kill-server").Run()
	alive := func() bool {
		for _, pid := range harnesses {
			if syscall.Kill(pid, 0) == nil {
				return true
			}
		}
		return false
	}
	if !waitQuietly(15*time.Second, func() bool { return !alive() }) {
		for _, pid := range harnesses {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	// A stop hook that loses its daemon starts another, so stop every process running
	// this lab's binary until none is left.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		pids := l.aboardPIDs()
		if len(pids) == 0 {
			break
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
		waitQuietly(2*time.Second, func() bool { return len(l.aboardPIDs()) == 0 })
		for _, pid := range l.aboardPIDs() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	if pids := l.aboardPIDs(); len(pids) > 0 {
		t.Errorf("aboard processes still running after the test: %v", pids)
	}
	// Codex starts an app server of its own from the test's CODEX_HOME, which outlives
	// the session.
	l.stopProcesses(filepath.Join(l.dir, "codex-home"))
	if diff := configDiff(realConfig, configSums()); diff != "" {
		t.Errorf("the real harness config or Aboard state changed:\n%s", diff)
	}
	t.Logf("turns: about %d (%d prompts typed, %d bundles delivered)", l.typed+len(l.handed()), l.typed, len(l.handed()))
}

// stopProcesses stops every process whose command line contains path.
func (l *lab) stopProcesses(path string) {
	for _, pid := range pidsOf(path) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	waitQuietly(5*time.Second, func() bool { return len(pidsOf(path)) == 0 })
	for _, pid := range pidsOf(path) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// aboardPIDs lists the processes running this lab's aboard binary.
func (l *lab) aboardPIDs() []int { return pidsOf(l.bin) }

// pidsOf lists the processes whose command line contains path.
func pidsOf(path string) []int {
	out, _ := command(context.Background(), "pgrep", "-f", path).Output()
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// saveArtifacts writes each pane's full scrollback, the daemon and server logs, the board
// and doctor's checks to the artifacts directory, and logs where.
func (l *lab) saveArtifacts() {
	base := os.Getenv("LIVE_ARTIFACTS")
	if base == "" {
		abs, err := filepath.Abs("artifacts")
		if err != nil {
			return
		}
		base = abs
	}
	dir := filepath.Join(base, l.t.Name()+"-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // LIVE_ARTIFACTS is the person's choice
		l.t.Logf("save artifacts: %v", err)
		return
	}
	write := func(name string, data []byte) {
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o600) //nolint:gosec // LIVE_ARTIFACTS is the person's choice
	}
	for _, p := range l.panes {
		write("pane-"+p.name+".txt", []byte(p.scrollback()))
	}
	for name, path := range map[string]string{
		"daemon.log": filepath.Join(l.stateDir(), "daemon.log"),
		"server.log": filepath.Join(l.dataDir(), "server.log"),
	} {
		if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
			write(name, raw)
		}
	}
	// Claude Code's transcripts, when it kept its config in the scratch directory, show
	// every command an agent ran and its output.
	if l.claudeConfig != "" {
		transcripts, _ := filepath.Glob(filepath.Join(l.claudeConfig, "projects", "*", "*.jsonl"))
		for _, path := range transcripts {
			if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
				write("transcript-"+filepath.Base(filepath.Dir(path))+"-"+filepath.Base(path), raw)
			}
		}
	}
	// Codex's config shows which hooks it trusted, and the hook log which ran.
	hooks, _ := filepath.Glob(filepath.Join(l.dir, "*", ".codex", "hooks.json"))
	for _, path := range append(hooks, filepath.Join(l.dir, "codex-home", "config.toml"), l.codexHookLog()) {
		if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
			rel, _ := filepath.Rel(l.dir, path)
			write(strings.ReplaceAll(rel, string(filepath.Separator), "-"), raw)
		}
	}
	write("doctor.json", []byte(l.exec(context.Background(), l.human, "doctor", "--json").stdout))
	for _, agent := range []string{"writer", "reviewer"} {
		write("board-as-"+agent+".json", []byte(l.exec(context.Background(), l.human, "read", "--as", agent, "--json", "--limit", "200").stdout))
	}
	l.t.Logf("artifacts: %s", dir)
}

// result is one finished aboard command.
type result struct {
	args   []string
	stdout string
	stderr string
	code   int
}

func (r result) String() string {
	return fmt.Sprintf("aboard %s\nexit %d\nstdout:\n%s\nstderr:\n%s", strings.Join(r.args, " "), r.code, r.stdout, r.stderr)
}

// exec runs aboard in dir as the person would in a terminal of their own.
func (l *lab) exec(ctx context.Context, dir string, args ...string) result {
	cmd := command(ctx, l.bin, args...)
	cmd.Dir = dir
	cmd.Env = l.vars
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{args: args, stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		r.code = -1
		r.stderr += err.Error()
	}
	return r
}

// run runs aboard in the person's directory and fails the test unless it exits 0.
func (l *lab) run(args ...string) {
	l.t.Helper()
	l.runIn(l.human, args...)
}

func (l *lab) runIn(dir string, args ...string) result {
	l.t.Helper()
	r := l.exec(l.t.Context(), dir, args...)
	if r.code != 0 {
		l.t.Fatalf("command failed:\n%s", r)
	}
	return r
}

// decode runs aboard with --json and decodes its output into v.
func (l *lab) decode(dir string, v any, args ...string) {
	l.t.Helper()
	r := l.runIn(dir, append(args, "--json")...)
	if err := json.Unmarshal([]byte(r.stdout), v); err != nil {
		l.t.Fatalf("output is not JSON: %v\n%s", err, r)
	}
}

// project makes a project directory set up the way a person sets up one project:
// aboard init --yes --scope project, with aboard commands allowed without a prompt.
func (l *lab) project(name, harness string) string {
	l.t.Helper()
	dir := filepath.Join(l.dir, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		l.t.Fatal(err)
	}
	var out struct {
		Applied   bool `json:"applied"`
		Harnesses []struct {
			Name    string `json:"name"`
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"harnesses"`
	}
	l.decode(dir, &out, "init", "--yes", "--scope", "project", "--harness", harness, "--allow-commands")
	if !out.Applied {
		l.t.Fatalf("aboard init in %s applied nothing", dir)
	}
	for _, h := range out.Harnesses {
		for _, c := range h.Changes {
			if !strings.HasPrefix(c.Path, dir+string(filepath.Separator)) {
				l.t.Fatalf("aboard init --scope project wrote %s, outside the project %s", c.Path, dir)
			}
		}
	}
	return dir
}

// pairCLI makes a board from the person's terminal: aboard pair makes writer, and
// aboard join with its join line makes reviewer. Neither is bound to a session yet.
func (l *lab) pairCLI() string {
	l.t.Helper()
	var pair struct {
		Board struct {
			Name string `json:"name"`
		} `json:"board"`
		Join struct {
			Line string `json:"line"`
		} `json:"join"`
	}
	l.decode(l.human, &pair, "pair")
	l.run("join", pair.Join.Line, "--json")
	return pair.Board.Name
}

// message is one board message as aboard read --json prints it.
type message struct {
	Seq  int       `json:"seq"`
	At   time.Time `json:"at"`
	Body string    `json:"body"`
	From struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"from"`
	ReplyToSeq *int `json:"reply_to_seq"`
	Urgent     bool `json:"urgent"`
}

// messages reads the board as agent, oldest first.
func (l *lab) messages(agent string) []message {
	l.t.Helper()
	msgs, r, ok := l.tryMessages(agent)
	if !ok {
		l.t.Fatalf("read the board:\n%s", r)
	}
	return msgs
}

// tryMessages reads the board as agent, and reports whether it could: the agent may not
// have joined yet.
func (l *lab) tryMessages(agent string) ([]message, result, bool) {
	r := l.exec(l.t.Context(), l.human, "read", "--as", agent, "--limit", "200", "--json")
	var page struct {
		Messages []message `json:"messages"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &page) != nil {
		return nil, r, false
	}
	return page.Messages, r, true
}

// say posts a message as agent from the person's terminal and returns it.
func (l *lab) say(agent string, args ...string) message {
	l.t.Helper()
	var out struct {
		Message message `json:"message"`
	}
	l.decode(l.human, &out, append([]string{"say", "--as", agent}, args...)...)
	return out.Message
}

// writerInbox is what writer's inbox holds, without acknowledging it.
func (l *lab) writerInbox() []message {
	l.t.Helper()
	var inbox struct {
		Messages []message `json:"messages"`
	}
	l.decode(l.human, &inbox, "inbox", "--peek", "--as", "writer")
	return inbox.Messages
}

// postAsOwner posts a message with the person's own login, as the web UI or any API
// client would: the CLI only posts as agents.
func (l *lab) postAsOwner(board, to, body string) message {
	l.t.Helper()
	token, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		l.t.Fatalf("read the owner login: %v", err)
	}
	payload, err := json.Marshal(map[string]any{"to": []string{to}, "body": body})
	if err != nil {
		l.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(l.t.Context(), http.MethodPost,
		"http://"+l.addr+"/v1/boards/"+board+"/messages", bytes.NewReader(payload))
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatalf("post as the owner: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		l.t.Fatalf("post as the owner: %s\n%s", resp.Status, raw)
	}
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		l.t.Fatalf("decode posted message: %v\n%s", err, raw)
	}
	return m
}

// serverVersion is the version the local server reports.
func (l *lab) serverVersion() string {
	l.t.Helper()
	req, err := http.NewRequestWithContext(l.t.Context(), http.MethodGet, "http://"+l.addr+"/v1/info", http.NoBody)
	if err != nil {
		l.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.t.Fatalf("GET /v1/info: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var info struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		l.t.Fatalf("decode /v1/info: %v", err)
	}
	return info.Version
}

// check is one of aboard doctor's checks.
type check struct {
	Name    string  `json:"name"`
	Level   string  `json:"level"`
	Code    *string `json:"code"`
	Message string  `json:"message"`
}

// doctor runs aboard doctor in dir. It exits 3 when a check is an error, which the
// caller asserts on, so any exit code is accepted here.
func (l *lab) doctor(dir string) []check {
	l.t.Helper()
	r := l.exec(l.t.Context(), dir, "doctor", "--json")
	var out struct {
		Checks []check `json:"checks"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		l.t.Fatalf("doctor output is not JSON: %v\n%s", err, r)
	}
	return out.Checks
}

// openSessions is how many sessions the delivery daemon has open.
func (l *lab) openSessions() int {
	l.t.Helper()
	var st struct {
		Daemon struct {
			OpenSessions int `json:"open_sessions"`
		} `json:"daemon"`
	}
	l.decode(l.human, &st, "status")
	return st.Daemon.OpenSessions
}

// handover is one "bundle handed" line in the daemon's log.
type handover struct {
	Time    time.Time `json:"time"`
	Msg     string    `json:"msg"`
	Session string    `json:"session"`
	Error   string    `json:"error"`
}

// handed lists every bundle the daemon handed to a session, oldest first.
func (l *lab) handed() []handover {
	raw, err := os.ReadFile(filepath.Join(l.stateDir(), "daemon.log"))
	if err != nil {
		return nil
	}
	var out []handover
	for _, line := range strings.Split(string(raw), "\n") {
		var h handover
		if json.Unmarshal([]byte(line), &h) == nil && h.Msg == "bundle handed" {
			out = append(out, h)
		}
	}
	return out
}

// handedAfter lists the bundles handed at or after t.
func (l *lab) handedAfter(t time.Time) []handover {
	var out []handover
	for _, h := range l.handed() {
		if !h.Time.Before(t) {
			out = append(out, h)
		}
	}
	return out
}

func (l *lab) pidFile(path string) int {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func (l *lab) daemonPID() int { return l.pidFile(filepath.Join(l.stateDir(), "daemon.pid")) }
func (l *lab) serverPID() int { return l.pidFile(filepath.Join(l.dataDir(), "server.pid")) }

// The suite waits on real harness processes and model turns, which no fake clock
// controls, so it polls with a deadline. The no-sleeps rule in engineering/testing.md is
// about our own code, which the other test levels drive with a fake clock.
const pollEvery = 250 * time.Millisecond

// waitFor polls cond until it holds, and fails the test after timeout.
func (l *lab) waitFor(timeout time.Duration, what string, cond func() bool) {
	l.t.Helper()
	if !waitQuietly(timeout, cond) {
		l.t.Fatalf("timed out after %s waiting for %s", timeout, what)
	}
}

// neverWithin fails the test if cond becomes true within d.
func (l *lab) neverWithin(d time.Duration, what string, cond func() bool) {
	l.t.Helper()
	if waitQuietly(d, cond) {
		l.t.Fatalf("%s within %s, and it shouldn't have", what, d)
	}
}

// waitQuietly polls cond until it holds or timeout passes, and reports which.
func waitQuietly(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollEvery)
	}
}

// pgrep reports whether a process with exactly these arguments runs.
func pgrep(args string) bool {
	return command(context.Background(), "pgrep", "-x", "-f", args).Run() == nil
}

// pane is one harness running in a tmux window.
type pane struct {
	l    *lab
	name string
	dir  string
	// harness is the command the pane runs: claude or codex.
	harness string
}

func (p *pane) target() string { return "live:" + p.name }

// tmuxRun runs a tmux command on the lab's own tmux server.
func (l *lab) tmuxRun(args ...string) string {
	l.t.Helper()
	out, err := command(context.Background(), "tmux", append([]string{"-S", l.tmux}, args...)...).CombinedOutput()
	if err != nil {
		l.t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// start runs argv in a new tmux window in dir, with exactly the environment env.
func (l *lab) start(name, dir string, env, argv []string) *pane {
	l.t.Helper()
	var script strings.Builder
	// The harness runs in tmux, whatever terminal runs the suite.
	script.WriteString("#!/bin/sh\nexec env -i TERM=tmux-256color")
	for _, kv := range env {
		if !strings.HasPrefix(kv, "TERM=") {
			script.WriteString(" " + shellQuote(kv))
		}
	}
	for _, a := range argv {
		script.WriteString(" " + shellQuote(a))
	}
	script.WriteString("\n")
	path := filepath.Join(l.dir, "launch-"+name+".sh")
	if err := os.WriteFile(path, []byte(script.String()), 0o700); err != nil { //nolint:gosec // the launch script must be executable
		l.t.Fatal(err)
	}
	if len(l.panes) == 0 {
		l.tmuxRun("new-session", "-d", "-s", "live", "-x", "220", "-y", "50", "-n", name, "-c", dir, path)
		l.tmuxRun("set-option", "-g", "remain-on-exit", "on")
	} else {
		l.tmuxRun("new-window", "-t", "live:", "-n", name, "-c", dir, path)
	}
	p := &pane{l: l, name: name, dir: dir, harness: argv[0]}
	l.panes = append(l.panes, p)
	return p
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// screen is what the pane shows now, with wrapped lines joined.
func (p *pane) screen() string {
	out, err := command(context.Background(), "tmux", "-S", p.l.tmux, "capture-pane", "-p", "-J", "-t", p.target()).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// scrollback is everything the pane has shown.
func (p *pane) scrollback() string {
	out, err := command(context.Background(), "tmux", "-S", p.l.tmux, "capture-pane", "-p", "-J", "-S", "-", "-t", p.target()).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// keys sends tmux key names, such as Enter or Down.
func (p *pane) keys(keys ...string) {
	p.l.tmuxRun(append([]string{"send-keys", "-t", p.target()}, keys...)...)
}

// typeInto types text into the harness and submits it. The text and the Enter are sent
// separately, and Enter only once the text shows, or a harness takes the Enter as part of
// a paste.
func (p *pane) typeInto(text string) {
	p.l.t.Helper()
	p.l.tmuxRun("send-keys", "-t", p.target(), "-l", text)
	prefix := text
	if len(prefix) > 30 {
		prefix = prefix[:30]
	}
	waitQuietly(5*time.Second, func() bool { return strings.Contains(p.screen(), prefix) })
	p.keys("Enter")
	p.l.typed++
}

// pid is the harness's process id: the launch script execs it in the pane.
func (p *pane) pid() int {
	p.l.t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(p.l.tmuxRun("display-message", "-p", "-t", p.target(), "#{pane_pid}")))
	if err != nil {
		p.l.t.Fatalf("read the pane's process: %v", err)
	}
	return pid
}
