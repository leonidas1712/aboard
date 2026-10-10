//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// killDaemon kills a home's delivery daemon at once, as a crash would.
func killDaemon(t *testing.T, e *env) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.stateDir(), "daemon.pid"))
	if err != nil {
		t.Fatalf("no daemon pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}

// goneText is what status, doctor and swarm say of an agent that can't reach its board.
func goneText(agent, board string) string {
	return agent + " can't reach " + board + " any more: the board is gone or hidden from its person, or the agent was removed from it"
}

// doctorCheck returns the doctor check with code, or nil.
func doctorCheck(t *testing.T, e *env, code string) map[string]any {
	t.Helper()
	r := e.runExit("doctor", "--json")
	for _, c := range field(t, r.json(t), "checks").([]any) {
		if m := c.(map[string]any); m["code"] == code {
			return m
		}
	}
	return nil
}

// When a person is removed from a board, their agent's delivery stops with a problem
// that status and doctor name, with the next step; adding the person back doesn't
// revive the agent, while a new agent of theirs works.
func TestAgentWhosePersonWasRemovedSaysItCantReachTheBoard(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	tm.admin.run("pair", "--name", "writer", "--json")
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	tm.admin.run("board", "add", "@maya")
	tm.link(maya, board)
	line := field(t, maya.run("invite", "--json").json(t), "join_line").(string)
	s := maya.claudeSession("s-maya")
	agent := field(t, s.run("join", line, "--json").json(t), "agent.name").(string)
	if c := doctorCheck(t, maya, "board_gone"); c != nil {
		t.Fatalf("doctor before the removal: %v", c)
	}

	tm.admin.run("board", "remove", "@maya")
	// The session's next idle wait reads the agent's inbox and finds the board gone.
	s.hook("prompt", `"prompt":"work"`)
	s.startHook("stop")
	var check map[string]any
	eventually(t, 5*time.Second, "doctor to name the board the agent lost", func() bool {
		check = doctorCheck(t, maya, "board_gone")
		return check != nil
	})
	if check["level"] != "error" || check["message"] != goneText(agent, board) ||
		check["fix"] != "join again with a new agent (aboard join) if the person still belongs on the board" {
		t.Fatalf("doctor's check: %v", check)
	}
	st := s.run("status", "--json").json(t)
	matchesCLISpec(t, "StatusOutput", st)
	stopped := field(t, st, "daemon.stopped_agents").([]any)
	if len(stopped) != 1 || field(t, stopped[0], "name") != agent || field(t, stopped[0], "board") != board || field(t, stopped[0], "reason") != "board_gone" {
		t.Fatalf("status's stopped agents: %v", st)
	}
	if text := s.run("status").stdout; !strings.Contains(text, goneText(agent, board)+". Join again with a new agent (aboard join) if the person still belongs on it.") {
		t.Fatalf("status:\n%s", text)
	}

	// Added back, maya's old agent stays gone: a new daemon, which reads every inbox
	// again, finds the board gone for it too.
	tm.admin.run("board", "add", "@maya")
	line = field(t, maya.run("invite", "--json").json(t), "join_line").(string)
	killDaemon(t, maya)
	s.hook("prompt", `"prompt":"more work"`)
	again := s.startHook("stop")
	eventually(t, 5*time.Second, "the new daemon to name the board the agent lost", func() bool {
		c := doctorCheck(t, maya, "board_gone")
		return c != nil && c["message"] == goneText(agent, board)
	})
	if !again.running(300 * time.Millisecond) {
		t.Fatalf("the old agent's session was handed something after maya came back\n%s", again.wait(time.Second))
	}

	// The daemon keeps the stopped agent by name, which stays safe because the server
	// never gives a removed agent's name to a new seat.
	fresh := maya.claudeSession("s-maya-2")
	if r := fresh.runExit("join", line, "--name", agent, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "name_taken" {
		t.Fatalf("a new agent taking the removed agent's name:\n%s", r)
	}
	// A new agent of maya's, in a new session, takes a seat and gets messages.
	newAgent := field(t, fresh.run("join", line, "--json").json(t), "agent.name").(string)
	if newAgent == agent {
		t.Fatalf("the new agent took the old agent's name %s", agent)
	}
	wait := fresh.startHook("stop")
	if !wait.running(300 * time.Millisecond) {
		t.Fatalf("the new agent's stop hook returned without a message\n%s", wait.wait(time.Second))
	}
	tm.admin.run("say", "--as", "writer", "--to", "@"+newAgent, "Welcome back.")
	if woke := wait.wait(5 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "Welcome back.") {
		t.Fatalf("the new agent's session should wake with the message\n%s", woke)
	}
}

// When the swarm's person is taken off the board, swarm ps and show say the seat can't
// reach it, and swarm up refuses the seat before starting anything, even once the person
// is back; a new name in the board file gives a new seat that works.
func TestSwarmSaysWhenItsBoardIsGone(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: gone\nagents:\n  - {name: worker, harness: claude-code, launcher: headless}\n")
	s.run("swarm", "up", "--json")
	s.run("up")
	s.run("servers", "use", "local")
	tm := &team{t: t, admin: s.env}
	kim := tm.person("kim")
	s.run("board", "add", "@kim", "--board", "gone")
	s.run("board", "owner", "@kim", "--board", "gone")
	tm.link(kim, "gone")
	kim.run("board", "remove", "@alex")

	out := s.run("swarm", "ps", "--json").json(t)
	matchesCLISpec(t, "SwarmPsOutput", out)
	if ag := agentsByName(t, out)["worker"]; ag["seat_credential"] != "board_gone" {
		t.Fatalf("the worker's seat after alex was removed: %v", ag)
	}
	if ps := s.run("swarm", "ps").stdout; !strings.Contains(ps, "worker can't reach gone any more: the board is gone or hidden from you, or the agent was removed from it.") ||
		!strings.Contains(ps, "give it a new name in the board file") {
		t.Fatalf("swarm ps after alex was removed:\n%s", ps)
	}
	if show := s.run("swarm", "show").stdout; !strings.Contains(show, "gone: the board is gone or hidden from you, or this agent was removed from it") {
		t.Fatalf("swarm show after alex was removed:\n%s", show)
	}

	s.run("swarm", "down")
	s.run("board", "add", "@alex", "--board", "gone")
	started := time.Now()
	r := s.runExit("swarm", "up", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "seat_board_gone" || field(t, r.json(t), "error.details.board") != "gone" ||
		!strings.Contains(r.stdout, "new name in the board file") {
		t.Fatalf("swarm up with a seat whose board is gone:\n%s", r)
	}
	if time.Since(started) > 20*time.Second {
		t.Fatalf("swarm up took %s to refuse the seat", time.Since(started))
	}
	if ag := agentsByName(t, s.run("swarm", "ps", "--json").json(t))["worker"]; ag["state"] == "running" {
		t.Fatalf("swarm up started the gone seat's session: %v", ag)
	}

	s.writeBoardFile("board: gone\nagents:\n  - {name: worker-2, harness: claude-code, launcher: headless}\n")
	s.run("swarm", "up", "--json")
	if ag := agentsByName(t, s.run("swarm", "ps", "--json").json(t))["worker-2"]; ag["seat_credential"] != "works" {
		t.Fatalf("the new seat: %v", ag)
	}
}

// swarm up for a board the person can't see, whose name a private board holds, says the
// board may be hidden from them and how to get on it.
func TestSwarmUpSaysAHiddenBoardMayHoldItsName(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.run("up")
	s.run("servers", "use", "local")
	tm := &team{t: t, admin: s.env}
	kim := tm.person("kim")
	status, v := tm.call("POST", "/v1/boards", tm.key(kim), map[string]any{"name": "secret", "template": "general", "visibility": "private"})
	if status != http.StatusCreated {
		t.Fatalf("kim creates a private board: %d %v", status, v)
	}
	s.writeBoardFile("board: secret\nagents:\n  - {name: worker, harness: claude-code, launcher: headless}\n")
	r := s.runExit("swarm", "up", "--json")
	out := r.json(t)
	if r.code != 1 || errorCode(t, out) != "board_name_taken" || field(t, out, "error.details.board") != "secret" ||
		!strings.Contains(field(t, out, "error.message").(string), "may exist but be hidden from you") ||
		!strings.Contains(field(t, out, "error.hint").(string), "Ask one of its owners to add you") {
		t.Fatalf("swarm up for a hidden board's name:\n%s", r)
	}
}
