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
)

// binary is the aboard executable built once for all tests.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aboard-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "aboard")
	build := exec.Command("go", "build", "-race", "-o", binary, "./server/cmd/aboard")
	build.Dir = ".."
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build aboard:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// env is one isolated machine: its own home, working directory and local server port.
type env struct {
	t    *testing.T
	home string
	dir  string
	addr string
	vars []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, home: home, dir: dir, addr: freeAddr(t)}
	e.vars = []string{
		"HOME=" + home,
		"USER=alex",
		"PATH=" + os.Getenv("PATH"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"ABOARD_LOCAL_ADDR=" + e.addr,
	}
	t.Cleanup(e.stopServer)
	return e
}

// port is the local server's port, as shown in URLs and join lines.
func (e *env) port() string {
	_, port, _ := net.SplitHostPort(e.addr)
	return port
}

func (e *env) dataDir() string {
	return filepath.Join(e.home, ".local", "share", "aboard")
}

// stopServer stops the background server this env started, if any.
func (e *env) stopServer() {
	raw, err := os.ReadFile(filepath.Join(e.dataDir(), "server.pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
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
	cmd := exec.Command(binary, args...)
	cmd.Dir = e.dir
	cmd.Env = e.vars
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
