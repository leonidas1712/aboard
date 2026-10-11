//go:build e2e

package e2e

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexJoinAndDeliveryUseTheSessionHomeAcrossRestarts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	home := filepath.Join(e.home, "codex home")
	e.vars = append(e.vars, "FAKE_CODEX_EXPECT_HOME="+home)
	log, err := os.Create(filepath.Join(e.home, "foreground-daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(e.bin, "daemon")
	cmd.Env = append(append([]string{}, e.vars...), "CODEX_HOME="+filepath.Join(e.home, "bogus"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	eventually(t, 10*time.Second, "foreground daemon socket", func() bool {
		c, err := net.Dial("unix", e.socketPath())
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	writer := e.claudeSession("home-writer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	id := "019a0000-0000-7000-8000-0000000000e1"
	s := &session{e: e, harness: "codex", id: id, vars: []string{"CODEX_THREAD_ID=" + id, "CODEX_HOME=" + home}}
	// Join is the first command: no session-start hook has registered its home.
	s.run("join", line, "--name", "reviewer")
	writer.run("say", "--to", "@reviewer", "before restart")
	waitForHomeMessage(t, e, id, "before restart")
	killDaemon(t, e)
	e.run("daemon", "start")
	conn, err := net.Dial("unix", e.socketPath())
	if err != nil {
		t.Fatal(err)
	}
	// An older client's register omits codex_home and still validates in the saved home.
	if err := json.NewEncoder(conn).Encode(map[string]any{"v": 1, "op": "register", "harness": "codex", "session": id}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := json.NewDecoder(conn).Decode(&reply); err != nil || reply["error"] != nil {
		t.Fatalf("old-client register: %v, %v", reply, err)
	}
	_ = conn.Close()
	writer.run("say", "--to", "@reviewer", "after restart")
	// No request from s has refreshed its home since the daemon restarted.
	waitForHomeMessage(t, e, id, "after restart")
	newHome := filepath.Join(e.home, "resumed home")
	// The fake harness requires the selected home on both thread/read and queue.
	killDaemon(t, e)
	e.vars = append(e.vars, "FAKE_CODEX_EXPECT_HOME="+newHome)
	s.vars = []string{"CODEX_THREAD_ID=" + id, "CODEX_HOME=" + newHome}
	s.run("resume", "reviewer")
	writer.run("say", "--to", "@reviewer", "after resume")
	waitForHomeMessage(t, e, id, "after resume")
}

func TestFirstCommandJoinsNamedBoardWithCustomCodexHome(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := tm.person("maya")
	board := tm.newBoard(tm.admin, "open")
	home := filepath.Join(person.home, "custom codex")
	person.vars = append(person.vars, "FAKE_CODEX_EXPECT_HOME="+home)
	s := &session{
		e: person, harness: "codex", id: "019a0000-0000-7000-8000-0000000000e2",
		vars: []string{"CODEX_THREAD_ID=019a0000-0000-7000-8000-0000000000e2", "CODEX_HOME=" + home},
	}
	out := s.run("join", "--board", board, "--json").json(t)
	if field(t, out, "use.bound_session") != "codex:"+s.id {
		t.Fatalf("custom-home named join did not bind this session: %v", out)
	}
}

func waitForHomeMessage(t *testing.T, e *env, id, text string) {
	t.Helper()
	eventually(t, 10*time.Second, text+" queued in the session home", func() bool {
		for _, call := range e.fakeCodexCalls() {
			if call["thread"] == id && strings.Contains(call["message"], text) {
				return true
			}
		}
		return false
	})
}
