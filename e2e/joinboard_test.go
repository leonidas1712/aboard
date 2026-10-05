//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credentialsOf reads a home's saved agent credentials.
func credentialsOf(t *testing.T, e *env) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Agents
}

// A session joins a board its person can see by name, with no code: the daemon asks
// the server through the machine's delegation, saves the seat with its member id, and
// binds it. Joining again from the same session gets the same seat with a new token,
// and the earlier token stops; boards in the session marks the seat.
func TestASessionJoinsABoardByName(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(tm.admin, "open")
	s := maya.claudeSession("s-join-board")

	out := s.run("join", "--board", board, "--json").json(t)
	matchesCLISpec(t, "JoinOutput", out)
	agentID, _ := field(t, out, "agent.id").(string)
	if out["via"] != "delegation" || out["reused"] != false || field(t, out, "agent.owner") != "maya" ||
		field(t, out, "use.bound_session") != "claude-code:s-join-board" || !strings.HasPrefix(agentID, "mem_") {
		t.Fatalf("join --board: %v", out)
	}
	creds := credentialsOf(t, maya)
	if len(creds) != 1 || creds[0]["member_id"] != agentID || creds[0]["board"] != board {
		t.Fatalf("credentials: %v", creds)
	}
	first, _ := creds[0]["token"].(string)
	if status, _ := tm.call("GET", "/v1/me", first, nil); status != 200 {
		t.Fatalf("the saved token: %d", status)
	}
	if st := s.run("status", "--json").json(t); st["agent"] != field(t, out, "agent.name") {
		t.Fatalf("status after the join: %v", st)
	}

	again := s.run("join", "--board", board)
	if !strings.HasPrefix(again.stdout, "This session is already "+field(t, out, "agent.name").(string)+" on "+board+".\n") {
		t.Fatalf("second join:\n%s", again)
	}
	creds = credentialsOf(t, maya)
	if len(creds) != 1 || creds[0]["member_id"] != agentID || creds[0]["token"] == first {
		t.Fatalf("credentials after the second join: %v", creds)
	}
	if status, _ := tm.call("GET", "/v1/me", first, nil); status != 401 {
		t.Fatalf("the earlier token still works: %d", status)
	}

	boards := s.run("boards", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", boards)
	if boards["session"] != "claude-code:s-join-board" || boards["as"] != nil || field(t, boards, "boards.0.seat") != field(t, out, "agent.name") {
		t.Fatalf("boards in the session: %v", boards)
	}

	// Another person's session with the same id gets a seat of its own.
	sam := tm.person("sam")
	theirs := sam.claudeSession("s-join-board").run("join", "--board", board, "--json").json(t)
	if field(t, theirs, "agent.id") == agentID || theirs["reused"] != false {
		t.Fatalf("sam's join: %v", theirs)
	}
}

// A join --board the person can't make is refused and binds nothing: a private board
// they aren't on looks like no board, and a server this machine has no key for needs a
// login.
func TestASessionsJoinByNameIsRefusedWithoutAccess(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	hidden := tm.newBoard(tm.admin, "private")
	s := maya.claudeSession("s-refused")
	r := s.e.exec(s.vars, "", "join", "--board", hidden, "--json")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "board_not_found") {
		t.Fatalf("a hidden board:\n%s", r)
	}
	if st := s.run("status", "--json").json(t); st["agent"] != nil {
		t.Fatalf("a refused join bound an agent: %v", st)
	}
	r = s.e.exec(s.vars, "", "join", "--board", hidden, "--server", "https://elsewhere.example.com", "--json")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "login_required") {
		t.Fatalf("a server with no key:\n%s", r)
	}
}

// In a terminal, join --board adds the person themselves to an open board, with no
// agent; a second run changes nothing, and a private board they aren't on is refused.
func TestJoinByNameInATerminalAddsThePerson(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(tm.admin, "open")
	out := maya.run("join", "--board", board, "--json").json(t)
	matchesCLISpec(t, "JoinPersonOutput", out)
	if out["added"] != true || field(t, out, "person.handle") != "maya" || out["board"] != board {
		t.Fatalf("join --board in a terminal: %v", out)
	}
	expectLines(t, maya.run("join", "--board", board), "You're on "+board+". Post with aboard say --board "+board+" \"…\", or in the board view.")
	if again := maya.run("join", "--board", board, "--json").json(t); again["added"] != false {
		t.Fatalf("again: %v", again)
	}
	if _, err := os.Stat(filepath.Join(maya.configDir(), "credentials.json")); err == nil {
		if creds := credentialsOf(t, maya); len(creds) != 0 {
			t.Fatalf("a terminal join made an agent: %v", creds)
		}
	}
	hidden := tm.newBoard(tm.admin, "private")
	if r := maya.runExit("join", "--board", hidden, "--json"); r.code == 0 || !strings.Contains(r.stdout, "board_not_found") {
		t.Fatalf("a private board:\n%s", r)
	}
}
