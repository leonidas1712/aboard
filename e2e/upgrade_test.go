//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// pidIn reads a process id file, or returns 0.
func pidIn(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func (e *env) daemonPID() int {
	return pidIn(filepath.Join(e.home, ".local", "state", "aboard", "daemon.pid"))
}

func (e *env) serverPID() int { return pidIn(filepath.Join(e.dataDir(), "server.pid")) }

// serverVersion is the version the local server reports in GET /v1/info.
func (e *env) serverVersion() string {
	e.t.Helper()
	return e.serverInfo().Version
}

// serverInfo is the build the local server reports in GET /v1/info.
func (e *env) serverInfo() (info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commit_time"`
},
) {
	e.t.Helper()
	resp, err := http.Get("http://" + e.addr + "/v1/info")
	if err != nil {
		e.t.Fatalf("GET /v1/info: %v", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		e.t.Fatalf("decode /v1/info: %v", err)
	}
	return info
}

// doctorChecks runs aboard doctor and returns its checks by name. A name with several
// checks keeps the last.
func (e *env) doctorChecks() map[string]map[string]any {
	e.t.Helper()
	r := e.runExit("doctor", "--json")
	checks := map[string]map[string]any{}
	for _, c := range field(e.t, r.json(e.t), "checks").([]any) {
		m := c.(map[string]any)
		checks[m["name"].(string)] = m
	}
	return checks
}

// After installing a new aboard, the first hook from it replaces the delivery daemon the
// old one started, and a message that was waiting for the session is still delivered.
func TestOlderDaemonIsReplacedAndAWaitingMessageStillArrives(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = oldBinary
	writer, reviewer := pairedClaudeSessions(t, e)
	if r := reviewer.hook("prompt", `"prompt":"work on something"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	writer.run("say", "--to", "@reviewer", "waiting across the upgrade")
	oldPID := e.daemonPID()
	if oldPID == 0 {
		t.Fatal("the older aboard started no daemon")
	}

	e.bin = binary // the new aboard is installed
	woke := reviewer.startHook("stop").wait(15 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "waiting across the upgrade") {
		t.Fatalf("the new stop hook should get the waiting message\n%s", woke)
	}
	if pid := e.daemonPID(); pid == 0 || pid == oldPID {
		t.Fatalf("daemon pid %d; the older daemon (pid %d) should have been replaced", pid, oldPID)
	}
	if d := e.doctorChecks()["daemon"]; d["level"] != "ok" {
		t.Fatalf("after the upgrade, doctor's daemon check: %v", d)
	}
}

// An upgrade that lands while a woken Claude Code turn runs doesn't wake the session a
// second time with the same message: the new aboard's hooks replace the old daemon, and
// the turn's next event still confirms the bundle the old daemon handed.
func TestUpgradeDuringAWakeDoesNotHandTheMessageAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = oldBinary
	writer, reviewer := pairedClaudeSessions(t, e)
	stop := reviewer.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatalf("stop hook returned without a message\n%s", stop.wait(time.Second))
	}
	writer.run("say", "--to", "@reviewer", "sent just before the upgrade")
	if woke := stop.wait(5 * time.Second); woke.code != 2 {
		t.Fatalf("the old stop hook should wake the session\n%s", woke)
	}
	oldPID := e.daemonPID()

	e.bin = binary // the new aboard is installed; the woken turn's hooks run it
	if r := reviewer.hook("prompt", `"prompt":"the bundle"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	if r := reviewer.hook("tool", `"tool_name":"Bash"`); r.code != 0 {
		t.Fatalf("tool hook failed\n%s", r)
	}
	if pid := e.daemonPID(); pid == 0 || pid == oldPID {
		t.Fatalf("daemon pid %d; the new hooks should have replaced the old daemon (pid %d)", pid, oldPID)
	}
	again := reviewer.startHook("stop")
	if !again.running(time.Second) {
		t.Fatalf("the turn's stop hook was handed the confirmed message again\n%s", again.wait(time.Second))
	}
	eventually(t, 5*time.Second, "the message to be acknowledged", func() bool {
		return len(field(t, e.run("inbox", "--as", "reviewer", "--peek", "--json").json(t), "messages").([]any)) == 0
	})
}

// After installing a new aboard, the first command that uses the local server replaces
// the one the old aboard started, and every board and message is still there.
func TestOlderLocalServerIsReplacedAndKeepsItsData(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = oldBinary
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	e.run("join", line)
	e.run("say", "--as", "writer", "--to", "@reviewer", "written by the old server")
	oldPID := e.serverPID()
	if v := e.serverVersion(); v != oldVersion {
		t.Fatalf("the older server reports version %q, want %q", v, oldVersion)
	}

	e.bin = binary
	inbox := e.run("inbox", "--as", "reviewer", "--json")
	if got := field(t, inbox.json(t), "messages.0.body"); got != "written by the old server" {
		t.Fatalf("message after the upgrade: %v\n%s", got, inbox)
	}
	if pid := e.serverPID(); pid == oldPID {
		t.Fatalf("the older server (pid %d) is still running", oldPID)
	}
	if v := e.serverVersion(); v == oldVersion {
		t.Fatalf("the server still reports the older version %q", v)
	}
}

// A local server from a build of the same version that predates commit reporting is
// replaced by a build that reports its commit, and every board and message is still
// there.
func TestLocalServerWithoutACommitIsReplacedByOneWithACommit(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = unstampedBinary
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	e.run("join", line)
	e.run("say", "--as", "writer", "--to", "@reviewer", "written before commit reporting")
	oldPID := e.serverPID()
	if info := e.serverInfo(); info.Commit != "" || info.CommitTime != "" {
		t.Fatalf("the unstamped server reports commit %q at %q, want neither", info.Commit, info.CommitTime)
	}

	e.bin = binary
	inbox := e.run("inbox", "--as", "reviewer", "--json")
	if got := field(t, inbox.json(t), "messages.0.body"); got != "written before commit reporting" {
		t.Fatalf("message after the upgrade: %v\n%s", got, inbox)
	}
	if pid := e.serverPID(); pid == oldPID {
		t.Fatalf("the server without a commit (pid %d) is still running", oldPID)
	}
	if info := e.serverInfo(); info.CommitTime == "" {
		t.Fatalf("the server reports no commit time after the upgrade: %+v", info)
	}
}

// An older aboard never replaces a newer daemon or server; it uses them.
func TestOlderAboardUsesANewerDaemonAndServer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	daemonPID, serverPID := e.daemonPID(), e.serverPID()

	e.bin = oldBinary
	if r := reviewer.hook("prompt", `"prompt":"hi"`); r.code != 0 {
		t.Fatalf("the older prompt hook failed against the newer daemon\n%s", r)
	}
	writer.run("say", "--to", "@reviewer", "from the older aboard")
	e.run("daemon", "start")
	if e.daemonPID() != daemonPID || e.serverPID() != serverPID {
		t.Fatalf("the older aboard replaced a newer part: daemon %d→%d, server %d→%d",
			daemonPID, e.daemonPID(), serverPID, e.serverPID())
	}
	if v := e.serverVersion(); v == oldVersion {
		t.Fatal("the server was downgraded")
	}
}

// Several commands from the new aboard that meet the older daemon at once replace it
// once, and all end up talking to the same new daemon.
func TestRacingCommandsReplaceAnOlderDaemonOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.bin = oldBinary
	e.run("daemon", "start")
	oldPID := e.daemonPID()

	e.bin = binary
	const n = 5
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = e.runExit("daemon", "start", "--json")
		}()
	}
	wg.Wait()
	pids := map[float64]bool{}
	for _, r := range results {
		if r.code != 0 {
			t.Fatalf("a racing command failed\n%s", r)
		}
		pids[field(t, r.json(t), "pid").(float64)] = true
	}
	now := e.daemonPID()
	if len(pids) != 1 || !pids[float64(now)] || now == oldPID {
		t.Fatalf("racing commands report daemons %v; want only the new one, %d (old was %d)", pids, now, oldPID)
	}
}

// installAt copies an aboard binary to path the way an installer does: a new file
// renamed over the old one.
func installAt(t *testing.T, from, path string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".new", raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Upgrading aboard at the same path leaves the installed skill and hooks byte for byte
// as they were, so the harnesses don't ask to trust the hooks again. An installed file
// that differs from what aboard init writes is reported by doctor, and aboard init
// --yes rewrites only Aboard's own parts.
func TestInitLeavesUnchangedHooksAloneAndDoctorFlagsChangedOnes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(e.home, ".claude", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"theme": "dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHooks := filepath.Join(e.home, ".codex", "hooks.json")
	claudeSkill := filepath.Join(e.home, ".claude", "skills", "aboard", "SKILL.md")
	installed := filepath.Join(e.home, "bin", "aboard")
	installAt(t, oldBinary, installed)
	e.bin = installed
	e.run("init", "--yes")
	before := []string{readFile(t, settings), readFile(t, codexHooks), readFile(t, claudeSkill)}

	installAt(t, binary, installed) // the upgrade
	again := e.run("init", "--yes", "--json").json(t)
	for _, h := range field(t, again, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if c.(map[string]any)["action"] != "unchanged" {
				t.Fatalf("init after an upgrade that changed no hooks: %v, want unchanged", c)
			}
		}
	}
	for i, path := range []string{settings, codexHooks, claudeSkill} {
		if readFile(t, path) != before[i] {
			t.Fatalf("%s changed on an upgrade that changed no hooks", path)
		}
	}
	names := []string{"claude_hooks", "codex_hooks", "claude_skill", "codex_skill"}
	checks := e.doctorChecks()
	for _, name := range names {
		if checks[name]["level"] != "ok" {
			t.Fatalf("doctor's %s check after the upgrade: %v", name, checks[name])
		}
	}

	// Files that differ from what init writes: an edited hook entry and skill in each harness.
	for _, path := range []string{settings, codexHooks} {
		edited := strings.Replace(readFile(t, path), `"timeout": 30`, `"timeout": 31`, 1)
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{claudeSkill, filepath.Join(e.home, ".agents", "skills", "aboard", "SKILL.md")} {
		if err := os.WriteFile(path, []byte(readFile(t, path)+"\nAn older paragraph.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	checks = e.doctorChecks()
	for name, code := range map[string]string{
		"claude_hooks": "hooks_outdated", "codex_hooks": "hooks_outdated",
		"claude_skill": "skill_outdated", "codex_skill": "skill_outdated",
	} {
		if c := checks[name]; c["code"] != code || c["fix"] != "run aboard init --yes" {
			t.Fatalf("doctor's %s check: %v, want %s with the fix aboard init --yes", name, c, code)
		}
	}
	update := e.run("init", "--yes", "--json").json(t)
	for _, h := range field(t, update, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if c.(map[string]any)["action"] != "update" {
				t.Fatalf("init after the edits: %v, want update", c)
			}
		}
	}
	checks = e.doctorChecks()
	for _, name := range names {
		if checks[name]["level"] != "ok" {
			t.Fatalf("after init --yes, doctor's %s check: %v", name, checks[name])
		}
	}
	if got := readFile(t, settings); !strings.Contains(got, `"theme": "dark"`) || got != before[0] {
		t.Fatalf("settings after init --yes should be as first installed:\n%s", got)
	}
}
