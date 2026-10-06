//go:build e2e

package e2e

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// A board's owner removes another person's agent; the agent's session is told it was
// removed, by whom in kind and when, and what its person can do; another member can't
// remove someone else's agent, and a person's command refuses inside a session.
func TestRemovingAnAgentEndsItsSession(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	tm.admin.run("pair", "--name", "writer", "--json")
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	tm.admin.run("board", "add", "@maya")
	tm.admin.run("board", "add", "@sam")
	tm.link(maya, board)
	tm.link(sam, board)
	line := field(t, maya.run("invite", "--json").json(t), "join_line").(string)
	s := maya.claudeSession("s-maya")
	agent := field(t, s.run("join", line, "--json").json(t), "agent.name").(string)
	s.run("say", "Notes are in the plan.")

	if r := sam.runExit("agent", "remove", agent, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "owner_required" {
		t.Fatalf("sam removing maya's agent:\n%s", r)
	}
	if r := s.runExit("agent", "remove", "writer", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
		t.Fatalf("a session running a person's command:\n%s", r)
	}

	out := tm.admin.run("agent", "remove", agent, "--json").json(t)
	matchesCLISpec(t, "AgentRemoveOutput", out)
	if field(t, out, "board") != board || field(t, out, "agent.removed_by") != "board_owner" || field(t, out, "agent.owner") != "maya" {
		t.Fatalf("the removal: %v", out)
	}

	r := s.runExit("say", "Still here?")
	today := time.Now().UTC().Format("2006-01-02")
	want := "Error (agent_removed): " + agent + " was removed from " + board + " by an owner of the board on " + today + ".\n" +
		"Hint: Ask your person to add a new agent: aboard join --board " + board + "\n"
	if r.code != 1 || r.stderr != want {
		t.Fatalf("the removed agent saying something:\n%s\nwant stderr:\n%s", r, want)
	}
	if read := tm.admin.run("read", "--as", "writer").stdout; !strings.Contains(read, "Notes are in the plan.") {
		t.Fatalf("the removed agent's message left the board:\n%s", read)
	}
	if r := tm.admin.runExit("agent", "remove", agent, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "agent_not_found" {
		t.Fatalf("removing it again:\n%s", r)
	}

	// Removing your own agent says so in plain text.
	if text := tm.admin.run("agent", "remove", "writer").stdout; text != "Removed writer from "+board+
		". Its messages stay on the board, and its sessions can't act there any more.\n" {
		t.Fatalf("the text of a removal:\n%s", text)
	}
}

// An agent leaves its own seat when its person asks, and is then told it left.
func TestAnAgentLeavesItsBoard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "--name", "writer", "--json").json(t), "join.line").(string)
	board := field(t, e.run("status", "--json").json(t), "board").(string)
	e.run("join", line, "--name", "reviewer", "--json")

	out := e.run("leave", "--as", "reviewer", "--json").json(t)
	matchesCLISpec(t, "LeaveOutput", out)
	if field(t, out, "agent.status") != "left" || field(t, out, "agent.removed_by") != "self" || field(t, out, "board") != board {
		t.Fatalf("leaving: %v", out)
	}
	r := e.runExit("inbox", "--as", "reviewer", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "agent_removed" ||
		!strings.Contains(field(t, r.json(t), "error.message").(string), "reviewer left "+board) {
		t.Fatalf("the agent after leaving:\n%s", r)
	}
	if text := e.run("leave", "--as", "writer").stdout; text != "writer left "+board+
		". Its messages stay on the board; a new agent there needs its person to add one (aboard join --board "+board+").\n" {
		t.Fatalf("the text of leaving:\n%s", text)
	}
}

// ageDisconnection makes the server's record say an agent's session ended days ago, as
// if the time had passed.
func ageDisconnection(t *testing.T, e *env, agent string, days int) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(e.dataDir(), "aboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	then := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02T15:04:05.000Z")
	res, err := db.Exec(`UPDATE members SET presence = 'no_session', presence_since = ?, presence_at = ? WHERE name = ? AND kind = 'agent'`, then, then, agent)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("aged %d agents called %s, want 1", n, agent)
	}
}

// Prune lists the agents disconnected for a while and removes them only with a yes;
// agents whose presence was never reported, or that disconnected recently, stay.
func TestPruneRemovesAgentsDisconnectedForAWhile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "--name", "writer", "--json").json(t), "join.line").(string)
	board := field(t, e.run("status", "--json").json(t), "board").(string)
	e.run("join", line, "--name", "reviewer", "--json")
	e.run("join", line, "--name", "editor", "--json")

	if text := e.run("agent", "prune").stdout; text != "None of your agents has been disconnected for at least 7 days.\n" {
		t.Fatalf("prune with nothing to do:\n%s", text)
	}
	ageDisconnection(t, e, "writer", 9)
	ageDisconnection(t, e, "reviewer", 8)
	ageDisconnection(t, e, "editor", 2)
	since := func(days int) string { return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02") }

	list := e.run("agent", "prune", "--disconnected-for", "7d", "--dry-run").stdout
	want := "These agents of yours have been disconnected for at least 7 days:\n" +
		"  writer     on " + board + "   since " + since(9) + "\n" +
		"  reviewer   on " + board + "   since " + since(8) + "\n"
	if list != want {
		t.Fatalf("the list:\n%s\nwant:\n%s", list, want)
	}
	r := e.runExit("agent", "prune", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" {
		t.Fatalf("prune without a yes:\n%s", r)
	}
	if r := e.runExit("agent", "prune", "--disconnected-for", "10m", "--json"); r.code != 2 {
		t.Fatalf("prune with too short a time:\n%s", r)
	}
	out := e.run("agent", "prune", "--yes", "--json").json(t)
	matchesCLISpec(t, "AgentPruneOutput", out)
	if n := len(field(t, out, "agents").([]any)); n != 2 || field(t, out, "dry_run") != false || field(t, out, "disconnected_for") != float64(7*24*3600) {
		t.Fatalf("the prune: %v", out)
	}
	for _, gone := range []string{"writer", "reviewer"} {
		if r := e.runExit("inbox", "--as", gone, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "agent_removed" {
			t.Fatalf("%s after the prune:\n%s", gone, r)
		}
	}
	e.run("inbox", "--as", "editor", "--json")
	if r := e.runExit("agent", "prune", "--all", "--dry-run", "--json"); r.code != 0 {
		t.Fatalf("the local server's person is its admin, so --all works:\n%s", r)
	}
}
