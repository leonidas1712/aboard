//go:build e2e

// Package e2e builds the real aboard binary and drives it the way a person or an agent
// would: CLI commands against a real local server with real SQLite, each test in its own
// temporary home directory.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// binary is the aboard executable built once for all tests.
var binary string

// oldBinary is aboard built with an older version stamp, playing an earlier install
// that a person upgrades from.
var oldBinary string

// oldVersion is oldBinary's version, older than the source's.
const oldVersion = "0.0.1"

// unstampedBinary is aboard at the source's version built without Git information,
// playing a build from before servers reported their commit.
var unstampedBinary string

// fakeBin holds the fake codex and claude binaries, first on every test's PATH.
var fakeBin string

// fakeHarness is a stand-in harness process that runs one hook and then stays up.
var fakeHarness string

// systemPath is this machine's PATH without the folders that hold a harness the tests
// leave out unless they ask for it: omp.
var systemPath string

// withoutCommand returns a PATH without the folders that hold an executable name.
func withoutCommand(path, name string) string {
	var keep []string
	for _, dir := range filepath.SplitList(path) {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			continue
		}
		keep = append(keep, dir)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

// builtVar names a directory that already holds the programs TestMain builds. A test
// that runs this test binary again as a helper sets it, so the helper doesn't build
// them a second time.
const builtVar = "ABOARD_E2E_BUILT"

// exitWithVar makes an aboard process stop once the process it names has exited. Each
// env sets it to this test process, so a local server, delivery daemon or hook a test
// started stops even when the test process is killed before its cleanups run.
const exitWithVar = "ABOARD_EXIT_WITH_PID"

func TestMain(m *testing.M) {
	// owned is the directory this process built the programs into and removes; empty
	// when a helper reuses the build of the test that runs it.
	var owned string
	dir := os.Getenv(builtVar)
	if dir == "" {
		tmp, err := os.MkdirTemp("", "aboard-e2e-bin-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "create temp dir:", err)
			os.Exit(1)
		}
		buildPrograms(tmp)
		dir, owned = tmp, tmp
	}
	binary = filepath.Join(dir, "aboard")
	// Named aboard too, as an installed binary is: aboard only stops processes by that name.
	oldBinary = filepath.Join(dir, "old", "aboard")
	unstampedBinary = filepath.Join(dir, "unstamped", "aboard")
	// A test's machine has no omp unless the test puts one there (the conformance kit
	// does, with a stand-in that only reports its version), so no test runs the person's
	// own omp or finds it installed.
	systemPath = withoutCommand(os.Getenv("PATH"), "omp")
	fakeHarness = filepath.Join(dir, "fakeharness")
	fakeBin = filepath.Join(dir, "fakebin")
	go exitWithParent(owned)
	code := m.Run()
	if owned != "" {
		_ = os.RemoveAll(owned)
	}
	os.Exit(code)
}

// exitWithParent exits this test process once the process that started it (go test)
// is gone. Killing go test would otherwise leave the test binary running on its own
// until its timeout; exiting lets everything the tests started stop with it
// (exitWithVar). It removes the directory owned, if any, first.
func exitWithParent(owned string) {
	parent := os.Getppid()
	tick := time.NewTicker(200 * time.Millisecond)
	for range tick.C {
		if os.Getppid() != parent {
			fmt.Fprintln(os.Stderr, "e2e: the process that started the tests is gone; stopping")
			if owned != "" {
				_ = os.RemoveAll(owned)
			}
			os.Exit(1)
		}
	}
}

// buildPrograms builds aboard, its older and unstamped builds and the fake harnesses
// into dir, or exits.
func buildPrograms(dir string) {
	build := exec.Command("go", "build", "-race", "-o", filepath.Join(dir, "aboard"), "./server/cmd/aboard")
	build.Dir = ".."
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build aboard:", err)
		os.Exit(1)
	}
	old := exec.Command("go", "build", "-race", "-o", filepath.Join(dir, "old", "aboard"),
		"-ldflags", "-X github.com/leonidas1712/aboard/server/internal/cli.version="+oldVersion, "./server/cmd/aboard")
	old.Dir = ".."
	old.Stdout, old.Stderr = os.Stderr, os.Stderr
	if err := old.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build the older aboard:", err)
		os.Exit(1)
	}
	unstamped := exec.Command("go", "build", "-race", "-buildvcs=false", "-o", filepath.Join(dir, "unstamped", "aboard"), "./server/cmd/aboard")
	unstamped.Dir = ".."
	unstamped.Stdout, unstamped.Stderr = os.Stderr, os.Stderr
	if err := unstamped.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build aboard without Git information:", err)
		os.Exit(1)
	}
	fake := exec.Command("go", "build", "-o", filepath.Join(dir, "fakebin", "codex"), "./e2e/fakecodex")
	fake.Dir = ".."
	fake.Stdout, fake.Stderr = os.Stderr, os.Stderr
	if err := fake.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build fake codex:", err)
		os.Exit(1)
	}
	harness := exec.Command("go", "build", "-o", filepath.Join(dir, "fakeharness"), "./e2e/fakeharness")
	harness.Dir = ".."
	harness.Stdout, harness.Stderr = os.Stderr, os.Stderr
	if err := harness.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build fake harness:", err)
		os.Exit(1)
	}
	// The harnesses swarm up starts: one program that plays claude, codex and omp by the
	// name it runs as. And herdr's launcher, with a stand-in for herdr.
	for _, b := range []struct{ out, pkg string }{
		{filepath.Join(dir, "fakeagents", "claude"), "./e2e/fakeagent"},
		{filepath.Join(dir, "herdrbin", "herdr"), "./e2e/fakeherdr"},
		{filepath.Join(dir, "herdrbin", "aboard-launcher-herdr"), "./launchers/herdr"},
	} {
		build := exec.Command("go", "build", "-o", b.out, b.pkg)
		build.Dir = ".."
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "build", b.pkg+":", err)
			os.Exit(1)
		}
	}
	for _, name := range []string{"codex", "omp"} {
		if err := os.Link(filepath.Join(dir, "fakeagents", "claude"), filepath.Join(dir, "fakeagents", name)); err != nil {
			fmt.Fprintln(os.Stderr, "link the fake agent:", err)
			os.Exit(1)
		}
	}
	// A stand-in claude that only reports its version, so no test runs the person's own
	// Claude Code. FAKE_CLAUDE_VERSION plays an older one.
	fakeClaude := "#!/bin/sh\necho \"${FAKE_CLAUDE_VERSION:-2.1.288} (Claude Code)\"\n"
	if err := os.WriteFile(filepath.Join(dir, "fakebin", "claude"), []byte(fakeClaude), 0o755); err != nil { //nolint:gosec // a test program
		fmt.Fprintln(os.Stderr, "write fake claude:", err)
		os.Exit(1)
	}
}

// env is one isolated machine: its own home, working directory and local server port.
type env struct {
	t *testing.T
	// bin is the aboard binary commands and hooks run: binary unless a test installs
	// another one.
	bin  string
	home string
	dir  string
	addr string
	vars []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	// Resolve links in the temp path (on macOS /var is a link to /private/var), so paths
	// the binary prints match the paths the test expects.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, bin: binary, home: home, dir: dir, addr: freeAddr(t)}
	e.vars = []string{
		"HOME=" + home,
		"USER=alex",
		"PATH=" + fakeBin + string(os.PathListSeparator) + systemPath,
		"FAKE_CODEX_LOG=" + filepath.Join(home, "fake-codex-queue.jsonl"),
		"FAKE_CODEX_THREADS=" + filepath.Join(home, "fake-codex-threads.json"),
		"ABOARD_HOME=" + filepath.Join(home, "aboard"),
		"ABOARD_LOCAL_ADDR=" + e.addr,
		exitWithVar + "=" + strconv.Itoa(os.Getpid()),
	}
	t.Cleanup(e.stopServer)
	return e
}

// port is the local server's port, as shown in URLs and join lines.
func (e *env) port() string {
	_, port, _ := net.SplitHostPort(e.addr)
	return port
}

// aboardHome is the env's ABOARD_HOME, which holds all of aboard's files.
func (e *env) aboardHome() string { return filepath.Join(e.home, "aboard") }

func (e *env) dataDir() string   { return filepath.Join(e.aboardHome(), "data") }
func (e *env) configDir() string { return filepath.Join(e.aboardHome(), "config") }
func (e *env) stateDir() string  { return filepath.Join(e.aboardHome(), "state") }

// stopServer stops the background local server and delivery daemon this env started.
func (e *env) stopServer() {
	for _, pidFile := range []string{
		filepath.Join(e.dataDir(), "server.pid"),
		filepath.Join(e.stateDir(), "daemon.pid"),
	} {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}

// result is one finished command.
type result struct {
	args   []string
	stdout string
	stderr string
	code   int
}

func (r result) String() string {
	return fmt.Sprintf("aboard %s\nexit %d\nstdout:\n%s\nstderr:\n%s",
		strings.Join(r.args, " "), r.code, r.stdout, r.stderr)
}

// run runs aboard with args in the env's project directory and fails the test unless it
// exits 0.
func (e *env) run(args ...string) result {
	e.t.Helper()
	r := e.runExit(args...)
	if r.code != 0 {
		e.t.Fatalf("command failed:\n%s\nserver log:\n%s", r, e.serverLog())
	}
	return r
}

// runExit runs aboard with args and returns whatever happened.
func (e *env) runExit(args ...string) result {
	e.t.Helper()
	return e.exec(nil, "", args...)
}

// exec runs aboard with extra environment variables and standard input.
func (e *env) exec(extra []string, stdin string, args ...string) result {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = e.dir
	cmd.Env = append(append([]string{}, e.vars...), extra...)
	cmd.Stdin = strings.NewReader(stdin)
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
		e.t.Fatalf("run aboard %v: %v", args, err)
	}
	return r
}

func (e *env) serverLog() string {
	raw, err := os.ReadFile(filepath.Join(e.dataDir(), "server.log"))
	if err != nil {
		return "(no server log)"
	}
	return string(raw)
}

// lines returns stdout split into lines, without the trailing empty line.
func (r result) lines() []string {
	return strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
}

// json decodes stdout as a JSON object.
func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &v); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, r)
	}
	return v
}

// field walks a dotted path such as "join.line" or "messages.0.from.name".
func field(t *testing.T, v any, path string) any {
	t.Helper()
	for _, part := range strings.Split(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			v = node[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i >= len(node) {
				t.Fatalf("path %s: no element %s", path, part)
			}
			v = node[i]
		default:
			t.Fatalf("path %s: cannot index %T with %s", path, v, part)
		}
	}
	return v
}

// expectLines fails unless got matches want line by line.
func expectLines(t *testing.T, r result, want ...string) {
	t.Helper()
	got := r.lines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("output differs\nwant:\n%s\ngot:\n%s\n\n%s", strings.Join(want, "\n"), strings.Join(got, "\n"), r)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}
