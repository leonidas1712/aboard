//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// link points a person's working directory at a board on the team's server, as a
// .aboard file from pair or join would.
func (tm *team) link(e *env, board string) {
	tm.t.Helper()
	project := `{"server":{"name":"` + tm.url() + `","url":"` + tm.url() + `"},"board":"` + board + `"}`
	if err := os.WriteFile(filepath.Join(e.dir, ".aboard"), []byte(project), 0o600); err != nil {
		tm.t.Fatal(err)
	}
}

// newBoard creates a board with a person's key and returns its name.
func (tm *team) newBoard(e *env, visibility string) string {
	tm.t.Helper()
	status, v := tm.call("POST", "/v1/boards", tm.key(e), map[string]any{"template": "general", "visibility": visibility})
	if status != http.StatusCreated || v["visibility"] != visibility {
		tm.t.Fatalf("create a %s board: %d %v", visibility, status, v)
	}
	return v["name"].(string)
}

// agentToken joins a new agent of e's person to board through the API and returns its
// token.
func (tm *team) agentToken(e *env, board string) string {
	tm.t.Helper()
	status, v := tm.call("POST", "/v1/join", tm.key(e), map[string]any{"board": board, "role": "member"})
	if status != http.StatusCreated {
		tm.t.Fatalf("join an agent: %d %v", status, v)
	}
	return v["token"].(string)
}

// People on a board are listed with their board role; a member adds people but only an
// owner removes them; the last owner can't leave; a removed person and their agent lose
// the private board at once. An agent needs the board's permission to add people;
// removing people and changing access still belong to the person.
func TestABoardsPeopleFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	tm.person("kim")
	board := tm.newBoard(maya, "private")
	tm.link(maya, board)
	tm.link(sam, board)

	r := maya.run("board", "add", "@sam")
	expectLines(t, r, "Added sam to "+board+".")
	samAgent := tm.agentToken(sam, board)

	people := maya.run("board", "people", "--json").json(t)
	matchesCLISpec(t, "BoardPeopleOutput", people)
	expectLines(t, maya.run("board", "people"), board+" · private · 2 people", "  maya (owner)", "  sam", "    @member · disconnected")

	added := sam.run("board", "add", "kim", "--json").json(t)
	matchesCLISpec(t, "BoardAddOutput", added)
	if field(t, added, "person.board_role") != "member" {
		t.Fatalf("kim added by sam: %v", added)
	}
	if r := sam.runExit("board", "remove", "@kim", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "owner_required" {
		t.Fatalf("a member removing someone:\n%s", r)
	}
	if r := maya.runExit("board", "leave", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "last_owner" {
		t.Fatalf("the last owner leaving:\n%s", r)
	}
	if r := sam.runExit("board", "visibility", "open", "--yes", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "owner_required" {
		t.Fatalf("a member turning the board open:\n%s", r)
	}

	s := maya.claudeSession("s-people")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	status, before := tm.call("GET", "/v1/boards/"+board, tm.key(maya), nil)
	if status != http.StatusOK {
		t.Fatalf("board before refused addition: status %d, error %v", status, before["error"])
	}
	refused := s.runExit("board", "add", "@kim", "--board", board, "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "add_people_not_allowed" {
		t.Fatalf("agent adding people to a private board without opt-in: exit %d, error %v", refused.code, refused.json(t)["error"])
	}
	status, after := tm.call("GET", "/v1/boards/"+board, tm.key(maya), nil)
	if status != http.StatusOK || after["head_seq"] != before["head_seq"] {
		t.Fatal("refused agent addition changed the private board")
	}
	// Access changes inside the agent's session are still handed to the person.
	for _, args := range [][]string{{"board", "remove", "@sam"}, {"board", "visibility", "open"}, {"board", "leave"}} {
		r := s.e.exec(s.vars, "", append(args, "--json")...)
		if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
			t.Fatalf("%v in an agent's session:\n%s", args, r)
		}
	}

	removed := maya.run("board", "remove", "@sam", "--json").json(t)
	matchesCLISpec(t, "BoardRemoveOutput", removed)
	expectLines(t, maya.run("board", "remove", "@kim"), "Removed kim from "+board+".")
	if r := sam.runExit("board", "people", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_not_found" {
		t.Fatalf("sam after removal:\n%s", r)
	}
	if status, v := tm.call("GET", "/v1/me/inbox", samAgent, nil); status != http.StatusForbidden || errorCode(t, v) != "agent_removed" {
		t.Fatalf("sam's agent after sam's removal: %d %v", status, v)
	}

	owner := maya.runExit("board", "owner", "@sam", "--json")
	if owner.code != 1 || errorCode(t, owner.json(t)) != "person_not_on_board" {
		t.Fatalf("making someone not on the board an owner:\n%s", owner)
	}
	maya.run("board", "add", "@sam")
	// Added back, sam returns, but his old agent stays removed; a new one works.
	if status, v := tm.call("POST", "/v1/boards/"+board+"/messages", samAgent, map[string]any{"body": "back again"}); status != http.StatusForbidden || errorCode(t, v) != "agent_removed" {
		t.Fatalf("sam's old agent after sam was added back: %d %v", status, v)
	}
	if status, v := tm.call("POST", "/v1/boards/"+board+"/messages", tm.agentToken(sam, board), map[string]any{"body": "a new agent"}); status != http.StatusCreated {
		t.Fatalf("sam's new agent: %d %v", status, v)
	}
	out := maya.run("board", "owner", "@sam", "--json").json(t)
	matchesCLISpec(t, "BoardOwnerOutput", out)
	if out["changed"] != true || field(t, out, "person.board_role") != "owner" {
		t.Fatalf("sam made owner: %v", out)
	}
	left := maya.run("board", "leave", "--json").json(t)
	matchesCLISpec(t, "BoardLeaveOutput", left)
	expectLines(t, sam.run("board", "people"), board+" · private · 1 person", "  sam (owner)", "    @member-2 · disconnected")
	sam.run("audit", "verify")
}

// Turning a board private cancels its join codes and hides it; turning it open again
// says how much becomes visible and needs a yes; then anyone may join and read it all.
func TestTurningABoardPrivateAndOpenFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	board := tm.newBoard(maya, "open")
	tm.link(maya, board)
	tm.link(sam, board)
	for _, body := range []string{"First finding.", "Second finding."} {
		if status, v := tm.call("POST", "/v1/boards/"+board+"/messages", tm.key(maya), map[string]any{"body": body}); status != http.StatusCreated {
			t.Fatalf("post: %d %v", status, v)
		}
	}
	line := field(t, maya.run("invite", "--json").json(t), "join_line").(string)

	// sam sees the open board and its people before joining.
	expectLines(t, sam.run("board", "people"), board+" · open · 1 person", "  maya (owner)")

	out := maya.run("board", "visibility", "private", "--json").json(t)
	matchesCLISpec(t, "BoardVisibilityOutput", out)
	if out["join_codes_canceled"] != float64(1) || out["after"] != "private" {
		t.Fatalf("turning private: %v", out)
	}
	if r := sam.runExit("join", line, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "join_code_invalid" {
		t.Fatalf("a join code after the board turned private:\n%s", r)
	}
	if r := sam.runExit("board", "people", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_not_found" {
		t.Fatalf("sam after private:\n%s", r)
	}

	r := maya.runExit("board", "visibility", "open")
	if r.code != 1 || !strings.Contains(r.stderr, "Error (confirmation_required): "+board+" is private. Making it open shows its whole history to every person on "+tm.url()+": 2 messages and 0 files.") ||
		!strings.Contains(r.stderr, "aboard board visibility open --board "+board+" --yes") {
		t.Fatalf("turning open without a yes:\n%s", r)
	}
	expectLines(t, maya.run("board", "visibility", "open", "--yes"), board+" is open now: every person on "+tm.url()+" can see it and join it.")
	expectLines(t, maya.run("board", "visibility", "open"), board+" is already open.")

	// sam joins by adding himself and reads the whole history.
	expectLines(t, sam.run("board", "add", "@sam"), "Added sam to "+board+".")
	status, v := tm.call("GET", "/v1/boards/"+board+"/messages", tm.key(sam), nil)
	if status != http.StatusOK || len(v["messages"].([]any)) != 2 {
		t.Fatalf("sam reading the history: %d %v", status, v)
	}
	maya.run("audit", "verify")
}

// A server from before boards were open or private keeps working: its boards are open,
// owned by their creator, the server's first person.
func TestUpgradeMakesExistingBoardsOpenAndOwned(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	tok := installSchema10(t, e)
	e.run("up")
	status, v := callAPI(t, "http://"+e.addr, "GET", "/v1/boards/writer-reviewer", tok.Owner, nil)
	if status != http.StatusOK || v["visibility"] != "open" || v["on_board"] != true {
		t.Fatalf("the board after the upgrade: %d %v", status, v)
	}
	expectLines(t, e.run("board", "people"), "writer-reviewer · open · 1 person", "  alex (owner)", "    @writer · disconnected", "    @reviewer · disconnected")
	e.run("audit", "verify")
}

// Only an admin's own key changes who may create boards; when it says admins, a member
// can't create one.
func TestBoardCreationCanBeLimitedToAdmins(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	if status, v := tm.call("PATCH", "/v1/settings", tm.key(maya), map[string]any{"board_creation": "admins"}); status != http.StatusForbidden || errorCode(t, v) != "server_admin_required" {
		t.Fatalf("a member changing the setting: %d %v", status, v)
	}
	if status, v := tm.call("PATCH", "/v1/settings", tm.admin.browserLogin(), map[string]any{"board_creation": "admins"}); status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
		t.Fatalf("a browser changing the setting: %d %v", status, v)
	}
	if status, v := tm.call("PATCH", "/v1/settings", tm.key(tm.admin), map[string]any{"board_creation": "admins"}); status != http.StatusOK || v["board_creation"] != "admins" {
		t.Fatalf("the admin changing the setting: %d %v", status, v)
	}
	if status, v := tm.call("POST", "/v1/boards", tm.key(maya), map[string]any{"template": "general"}); status != http.StatusForbidden || errorCode(t, v) != "board_creation_restricted" {
		t.Fatalf("a member creating a board: %d %v", status, v)
	}
	tm.newBoard(tm.admin, "open")
}
