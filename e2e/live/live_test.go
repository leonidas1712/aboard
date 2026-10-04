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
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// builtVar names a directory that already holds the aboard binary TestMain builds. A
// test that runs this test binary again as a helper sets it, so the helper doesn't build
// it a second time.
const builtVar = "ABOARD_LIVE_BUILT"

// exitWithVar makes an aboard process stop once the process it names has exited. Each
// lab sets it to this test process, so a local server, delivery daemon or hook a test
// started stops even when the test process is killed before its cleanups run.
const exitWithVar = "ABOARD_EXIT_WITH_PID"

func runTests(m *testing.M) int {
	realConfig = configSums()
	// owned is the directory this process built aboard into and removes; empty when a
	// helper reuses the build of the test that runs it.
	var owned string
	dir := os.Getenv(builtVar)
	if dir == "" {
		tmp, err := os.MkdirTemp("", "aboard-live-bin-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "create temp dir:", err)
			return 1
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if err := buildAboard(context.Background(), filepath.Join(tmp, "aboard"), ""); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		dir, owned = tmp, tmp
	}
	newBinary = filepath.Join(dir, "aboard")
	go exitWithParent(owned)
	began := time.Now()
	code := m.Run()
	printTimings(os.Stdout, time.Since(began))
	if err := saveResults(); err != nil {
		fmt.Fprintln(os.Stderr, "live: save the results for the harness table:", err)
		code = 1
	}
	if diff := configDiff(realConfig, configSums()); diff != "" {
		fmt.Fprintln(os.Stderr, "live: the real harness config or Aboard state changed during the run:\n"+diff)
		return 1
	}
	return code
}

// exitWithParent exits this test process once the process that started it (go test)
// is gone. Killing go test would otherwise leave the test binary running on its own
// until its timeout, spending model turns; exiting lets each lab's watchdog stop what
// the lab started. It removes the directory owned, if any, first.
func exitWithParent(owned string) {
	parent := os.Getppid()
	tick := time.NewTicker(200 * time.Millisecond)
	for range tick.C {
		if os.Getppid() != parent {
			fmt.Fprintln(os.Stderr, "live: the process that started the tests is gone; stopping")
			if owned != "" {
				_ = os.RemoveAll(owned)
			}
			os.Exit(1)
		}
	}
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

// configSums records the person's harness config files, the folders Aboard's setup or a
// harness could write into, and whether Aboard's state directories exist: a sha256 of
// each file's contents (and of each folder's whole tree), where each link points, or
// "absent". Live runs must leave every one of them as it was. A file rewritten with the
// same bytes counts as unchanged: apps such as a terminal's agent integration rewrite
// their own files while a run goes on.
func configSums() map[string]string {
	home, _ := os.UserHomeDir()
	sums := map[string]string{}
	for _, f := range []string{".claude/settings.json", ".codex/config.toml", ".codex/hooks.json", ".omp/agent/config.yml"} {
		path := filepath.Join(home, f)
		sums[path] = treeSum(path)
	}
	for _, d := range []string{".local/state/aboard", ".local/share/aboard", ".config/aboard"} {
		path := filepath.Join(home, d)
		if _, err := os.Stat(path); err == nil {
			sums[path] = "present"
		} else {
			sums[path] = "absent"
		}
	}
	// Harnesses install their commands into ~/.local/bin and keep versions beside it, so
	// every entry there and every installed Claude Code version must stay as it was.
	// omp's login is in ~/.omp/agent/agent.db, which omp itself changes as the person uses
	// it, so what is checked are its config and the folders a test could write into: its
	// extensions and skills. ~/.agents/skills is where Codex finds skills everywhere, and
	// ~/.claude/skills where Claude Code does. Each lab checks these again as it ends
	// (teardown).
	for _, d := range []string{
		".local/bin", ".local/share/claude/versions", ".omp/agent/extensions", ".omp/agent/skills",
		".agents/skills", ".claude/skills",
	} {
		entries, err := os.ReadDir(filepath.Join(home, d))
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(home, d, e.Name())
			sums[path] = treeSum(path)
		}
	}
	return sums
}

// fileSums caches each file's sha256 by its path, size and modification time, so the
// installed harness binaries, hundreds of megabytes each, are read once per run rather
// than at every test's end; a file whose size or time changed is read again.
var fileSums sync.Map

// treeSum describes path for configSums: "absent", where a link points, the sha256 of a
// file's contents, or, for a folder, the sha256 of every entry under it by name.
func treeSum(path string) string {
	info, err := os.Lstat(path)
	switch {
	case err != nil:
		return "absent"
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return "unreadable link"
		}
		return "link to " + target
	case info.IsDir():
		entries, err := os.ReadDir(path)
		if err != nil {
			return "unreadable folder"
		}
		h := sha256.New()
		for _, e := range entries {
			_, _ = fmt.Fprintf(h, "%s %s\n", e.Name(), treeSum(filepath.Join(path, e.Name())))
		}
		return "folder, sha256 " + hex.EncodeToString(h.Sum(nil))
	}
	key := fmt.Sprintf("%s %d %d", path, info.Size(), info.ModTime().UnixNano())
	if sum, ok := fileSums.Load(key); ok {
		if s, ok := sum.(string); ok {
			return s
		}
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "unreadable file"
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable file"
	}
	sum := "sha256 " + hex.EncodeToString(h.Sum(nil))
	fileSums.Store(key, sum)
	return sum
}

// configDiff lists every path whose checksum or presence changed.
func configDiff(before, after map[string]string) string {
	var b strings.Builder
	for path, sum := range before {
		if after[path] != sum {
			fmt.Fprintf(&b, "  %s: was %s, now %s\n", path, sum, after[path])
		}
	}
	for path, sum := range after {
		if _, ok := before[path]; !ok {
			fmt.Fprintf(&b, "  %s: appeared, %s\n", path, sum)
		}
	}
	return b.String()
}

// harnessMarkers are the variables a harness sets for what runs inside it. A harness the
// suite starts must not inherit them from the session that runs the suite: they would
// make aboard think it runs inside that session, and in a remote Claude Code container
// they make the nested Claude Code take over the outer session's id.
var harnessMarkers = regexp.MustCompile(`^(CLAUDE|CODEX|ABOARD|TMUX|OMP|PI_)[A-Z0-9_]*=`)

// terminalMarkers are the variables the person's terminal app sets to say which terminal
// a program draws to, and to let agents running in it report back to the app. A harness
// the suite starts draws to the lab's tmux, not to that terminal, and must never report
// to the person's app, so it doesn't inherit them.
var terminalMarkers = regexp.MustCompile(`^(TERM_PROGRAM|TERM_PROGRAM_VERSION|TERM_SESSION_ID|LC_TERMINAL|LC_TERMINAL_VERSION|` +
	`WT_SESSION|STY|__CFBundleIdentifier|(KITTY|GHOSTTY|WEZTERM|ITERM|VSCODE|ALACRITTY|WARP|ZELLIJ|CMUX|HERDR|ORCA)[A-Z0-9_]*)=`)

// cleanEnv is this process's environment without harness and terminal markers, keeping
// what the harnesses need for their login (HOME, ANTHROPIC_*, OPENAI_*,
// CLAUDE_CONFIG_DIR, and CLAUDE_CODE_OAUTH_TOKEN, which logs Claude Code in with a
// scratch config directory).
func cleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR="), strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN="):
		case harnessMarkers.MatchString(kv), terminalMarkers.MatchString(kv),
			strings.HasPrefix(kv, "XDG_CONFIG_HOME="), strings.HasPrefix(kv, "XDG_DATA_HOME="),
			strings.HasPrefix(kv, "XDG_STATE_HOME="), strings.HasPrefix(kv, "PATH="):
			continue
		}
		out = append(out, kv)
	}
	return out
}

// lab is one isolated machine for a test: aboard in its own directory, Aboard's state
// in an ABOARD_HOME in the scratch directory, a free port for the local server, and a
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
	// started counts the harnesses started in this lab.
	started int
	// claudeConfig is Claude Code's scratch config directory for this test.
	claudeConfig string
	// codexReady is true once codexHome has set up the test's CODEX_HOME for Codex.
	codexReady bool
	tmux       string
	panes      []*pane
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
	if err := os.MkdirAll(l.home(), 0o700); err != nil {
		t.Fatal(err)
	}
	l.vars = append(cleanEnv(),
		"PATH="+filepath.Dir(l.bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Everything the lab runs (aboard and its daemon, the harnesses and their hooks)
		// has a home folder of the lab's own, so a folder found from HOME, such as Codex's
		// ~/.agents/skills, is the lab's and never the person's.
		"HOME="+l.home(),
		"ABOARD_HOME="+state,
		"ABOARD_LOCAL_ADDR="+l.addr,
		// A harness updating itself during a test would change the person's own install,
		// so updates are off.
		"DISABLE_AUTOUPDATER=1",
		// Aboard's global setup follows these (D92) where a harness has them, so aboard
		// doctor and init look in the test's own folders here too.
		"CODEX_HOME="+filepath.Join(dir, "codex-home"),
		// omp's agent folder in the lab's scratch home for omp, never the person's ~/.omp.
		"PI_CODING_AGENT_DIR="+filepath.Join(dir, "omp-home", ".omp", "agent"),
		exitWithVar+"="+strconv.Itoa(os.Getpid()),
	)
	if err := os.MkdirAll(filepath.Join(dir, "codex-home"), 0o700); err != nil {
		t.Fatal(err)
	}
	l.claudeConfig = filepath.Join(dir, "claude-config")
	l.vars = append(l.vars, "CLAUDE_CONFIG_DIR="+l.claudeConfig)
	l.watchdog(sockDir)
	t.Cleanup(l.teardown)
	return l
}

// watchdogScript stops what a lab started once the test process is gone without having
// run its cleanups: interrupted, timed out or killed. It ends the lab's tmux server,
// whose panes run the harnesses, kills what is left of them, then every process whose
// command line names the lab's directory (its aboard and Codex's app server). Its
// arguments come from the environment, so its own command line names nothing it kills.
const watchdogScript = `
while kill -0 "$LAB_OWNER" 2>/dev/null; do sleep 1; done
panes=$(tmux -S "$LAB_TMUX" list-panes -a -F '#{pane_pid}' 2>/dev/null)
tmux -S "$LAB_TMUX" kill-server 2>/dev/null
for _ in 1 2 3 4 5; do
	running=
	for p in $panes; do kill -0 "$p" 2>/dev/null && running=1; done
	[ -z "$running" ] && break
	sleep 1
done
for p in $panes; do kill -KILL -- "-$p" "$p" 2>/dev/null; done
for sig in TERM TERM KILL; do
	pkill -"$sig" -f "$LAB_DIR" || break
	sleep 1
done
rm -rf "$LAB_SOCKETS"
`

// watchdog starts a process that stops what the lab starts if the test process dies
// before its cleanups run (watchdogScript). It runs in a process group of its own, so an
// interrupt from the terminal reaches the test but not the watchdog. The lab's cleanup
// stops it.
func (l *lab) watchdog(sockDir string) {
	l.t.Helper()
	cmd := command(context.Background(), "sh", "-c", watchdogScript)
	cmd.Env = append(os.Environ(),
		"LAB_OWNER="+strconv.Itoa(os.Getpid()),
		"LAB_TMUX="+l.tmux,
		"LAB_DIR="+l.dir,
		"LAB_SOCKETS="+sockDir,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		l.t.Fatalf("start the lab's watchdog: %v", err)
	}
	l.t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the script and its sleep
		_ = cmd.Wait()
	})
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

// givenAddrs are the addresses freeAddr has handed out in this run.
var givenAddrs = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

// freeAddr returns a local address no process listens on, and never the same one twice
// in a run: labs run in parallel, and a lab's server starts some time after its address
// is chosen, so the system could otherwise offer that port to another lab in between.
func freeAddr(t *testing.T) string {
	t.Helper()
	givenAddrs.Lock()
	defer givenAddrs.Unlock()
	for {
		var lc net.ListenConfig
		l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		_ = l.Close()
		if !givenAddrs.m[addr] {
			givenAddrs.m[addr] = true
			return addr
		}
	}
}

// home is the lab's home folder: HOME for everything the lab runs but omp, which has a
// scratch home of its own (ompHome).
func (l *lab) home() string { return filepath.Join(l.dir, "home") }

func (l *lab) stateDir() string  { return filepath.Join(l.dir, "state", "state") }
func (l *lab) dataDir() string   { return filepath.Join(l.dir, "state", "data") }
func (l *lab) configDir() string { return filepath.Join(l.dir, "state", "config") }

// teardown saves what explains a failure, stops every harness and aboard process the lab
// started, and checks the person's own config is untouched.
func (l *lab) teardown() {
	t := l.t
	if t.Failed() || os.Getenv("LIVE_KEEP") != "" {
		l.saveArtifacts()
	}
	l.measureHandovers()
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
	// Codex runs its hooks from an app server of its own, started from the test's
	// CODEX_HOME, which outlives the session; stopping it runs the session-end hook,
	// which can start a daemon, so it stops before aboard does.
	l.stopProcesses(filepath.Join(l.dir, "codex-home"))
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
	// Claude Code's transcripts, its subagents' included, show every command an agent ran
	// and its output.
	for _, path := range l.claudeTranscripts() {
		if raw, err := os.ReadFile(filepath.Clean(path)); err == nil {
			write("transcript-"+filepath.Base(filepath.Dir(path))+"-"+filepath.Base(path), raw)
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
	for _, agent := range []string{"writer", "reviewer", "claude", "claude-2"} {
		write("board-as-"+agent+".json", []byte(l.exec(context.Background(), l.human, "read", "--as", agent, "--json", "--limit", "200").stdout))
	}
	l.t.Logf("artifacts: %s", dir)
}

// claudeTranscripts lists every transcript Claude Code wrote in the test's config
// directory, its subagents' included.
func (l *lab) claudeTranscripts() []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(l.claudeConfig, "projects"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out
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
	// doctor names every file it reads, so it shows whether anything the lab runs looks in
	// the person's own home folder.
	if len(args) > 0 && args[0] == "doctor" {
		if paths := l.realHomePaths(r.stdout + r.stderr); len(paths) > 0 {
			l.t.Errorf("aboard doctor in the lab names paths in your own home folder, so the lab isn't isolated from it: %q\n%s", paths, r)
		}
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

// pairCLI makes a board from the person's terminal: aboard pair writer-reviewer makes
// writer, and aboard join with its join line makes reviewer. Neither is bound to a session yet.
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
	l.decode(l.human, &pair, "pair", "writer-reviewer")
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
	ReplyToSeq *int     `json:"reply_to_seq"`
	Urgent     bool     `json:"urgent"`
	To         []string `json:"to"`
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
func (l *lab) writerInbox() []message { return l.inbox("writer") }

// inbox is what agent's inbox holds, without acknowledging it.
func (l *lab) inbox(agent string) []message {
	l.t.Helper()
	var inbox struct {
		Messages []message `json:"messages"`
	}
	l.decode(l.human, &inbox, "inbox", "--peek", "--as", agent)
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
	// Board is the board of the messages handed or confirmed, and Began when handing a
	// bundle began.
	Board string    `json:"board"`
	Began time.Time `json:"began"`
	// Seqs are the messages handed, claimed or added at a tool boundary.
	Seqs []int `json:"seqs"`
	// Announced are the messages a tool boundary's notice named.
	Announced []int `json:"announced"`
}

// logged lists the daemon log lines with message msg, oldest first: "bundle handed",
// "tool boundary" (the owner's messages and the waiting notice added to a busy turn),
// "turn start" (what a starting turn was given) or "claimed by a command" (aboard say
// --wait-reply showed them).
func (l *lab) logged(msg string) []handover {
	raw, err := os.ReadFile(filepath.Join(l.stateDir(), "daemon.log"))
	if err != nil {
		return nil
	}
	var out []handover
	for _, line := range strings.Split(string(raw), "\n") {
		var h handover
		if json.Unmarshal([]byte(line), &h) == nil && h.Msg == msg {
			out = append(out, h)
		}
	}
	return out
}

// sessionStart is one "session started" line in the daemon's log: a session-start hook
// registered a session.
type sessionStart struct {
	Time    time.Time `json:"time"`
	Session string    `json:"session"`
	Source  string    `json:"source"`
	// Reopened is true when the session had closed and its harness resumed it.
	Reopened bool `json:"reopened"`
	// Agents is how many agents the session is bound to once registered.
	Agents int `json:"agents"`
}

// starts lists every session start in the daemon's log, oldest first.
func (l *lab) starts() []sessionStart {
	raw, err := os.ReadFile(filepath.Join(l.stateDir(), "daemon.log"))
	if err != nil {
		return nil
	}
	var out []sessionStart
	for _, line := range strings.Split(string(raw), "\n") {
		var s struct {
			Msg string `json:"msg"`
			sessionStart
		}
		if json.Unmarshal([]byte(line), &s) == nil && s.Msg == "session started" {
			out = append(out, s.sessionStart)
		}
	}
	return out
}

// reached returns when the message seq first reached its recipient's session, from the
// daemon's log: handed in a bundle, added at a tool boundary, or shown by aboard say
// --wait-reply. ok is false if it hasn't.
func (l *lab) reached(seq int) (at time.Time, how string, ok bool) {
	for _, msg := range []string{"bundle handed", "tool boundary", "turn start", "claimed by a command"} {
		for _, h := range l.logged(msg) {
			if slices.Contains(h.Seqs, seq) && h.Error == "" && (!ok || h.Time.Before(at)) {
				at, how, ok = h.Time, msg, true
			}
		}
	}
	return at, how, ok
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
	// d drives the harness the pane runs.
	d *driver
}

// driverFor returns the driver of the harness whose command is name.
func (l *lab) driverFor(name string) *driver {
	l.t.Helper()
	for _, d := range drivers(l.t) {
		if d.p.Command == name {
			return d
		}
	}
	l.t.Fatalf("no live driver runs %s", name)
	return nil
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
	l.started++
	path := l.launchScript(name, env, argv)
	if len(l.panes) == 0 {
		l.tmuxRun("new-session", "-d", "-s", "live", "-x", "220", "-y", "50", "-n", name, "-c", dir, path)
		l.tmuxRun("set-option", "-g", "remain-on-exit", "on")
	} else {
		l.tmuxRun("new-window", "-t", "live:", "-n", name, "-c", dir, path)
	}
	p := &pane{l: l, name: name, dir: dir, harness: argv[0], d: l.driverFor(argv[0])}
	l.panes = append(l.panes, p)
	return p
}

// respawn runs argv in the pane again, in its directory, once the harness that ran there
// has exited (panes stay open after their command exits), the way a person runs the
// harness again in the same terminal.
func (p *pane) respawn(env, argv []string) {
	p.l.t.Helper()
	path := p.l.launchScript(p.name+"-again", env, argv)
	p.l.tmuxRun("respawn-pane", "-t", p.target(), "-c", p.dir, path)
	p.l.waitFor(10*time.Second, p.name+": the pane to run again", func() bool { return !strings.Contains(p.screen(), "Pane is dead") })
}

// launchScript writes a script that runs argv with exactly the environment env, and
// returns its path.
func (l *lab) launchScript(name string, env, argv []string) string {
	l.t.Helper()
	var script strings.Builder
	// The harness runs in tmux, whatever terminal runs the suite. A pane run again still
	// shows what the harness before it left on the screen, which would look like the new
	// one is ready, so the screen is cleared first.
	script.WriteString("#!/bin/sh\nprintf '\\033[2J\\033[H'\nexec env -i TERM=tmux-256color")
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
	return path
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
