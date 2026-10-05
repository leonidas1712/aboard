//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// A person lists the boards they are on with their role and counts; --all adds the open
// boards they aren't on, with how to join; an admin also sees that private boards they
// aren't on exist, and nothing more.
func TestBoardsListsWhatEachPersonCanSee(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	secret := tm.newBoard(maya, "private")
	if status, v := tm.call("PATCH", "/v1/boards/"+secret, tm.key(maya), map[string]any{"title": "Secret plans"}); status != http.StatusOK {
		t.Fatalf("title: %d %v", status, v)
	}
	open := tm.newBoard(sam, "open")
	tm.link(maya, secret)
	tm.link(sam, open)
	tm.agentToken(maya, secret)

	out := maya.run("boards", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", out)
	if field(t, out, "boards.0.name") != secret || field(t, out, "boards.0.role") != "owner" ||
		field(t, out, "boards.0.agents") != float64(1) || field(t, out, "boards.0.default") != true || len(out["boards"].([]any)) != 1 {
		t.Fatalf("maya's boards: %v", out)
	}
	expectLines(t, maya.run("boards"),
		"Your boards on "+tm.url()+":",
		"  "+secret+` "Secret plans" · private · owner · 1 person · 1 agent · default`)

	all := maya.run("boards", "--all", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", all)
	expectLines(t, maya.run("boards", "--all"),
		"Boards you can see on "+tm.url()+":",
		"  "+secret+` "Secret plans" · private · owner · 1 person · 1 agent · default`,
		"  "+open+" · not joined · 1 person; join with aboard board add @me --board "+open)
	if len(all["hidden_boards"].([]any)) != 0 {
		t.Fatalf("a member's hidden boards: %v", all)
	}

	// Joining with @me puts maya on the open board.
	expectLines(t, maya.run("board", "add", "@me", "--board", open), "Added maya to "+open+".")

	// The admin sees the private board only as a fact.
	tm.link(tm.admin, "none")
	admin := tm.admin.run("boards", "--all", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", admin)
	if len(admin["hidden_boards"].([]any)) != 1 || field(t, admin, "hidden_boards.0.created_by.handle") != "maya" {
		t.Fatalf("the admin's hidden boards: %v", admin)
	}
	text := tm.admin.run("boards", "--all")
	if strings.Contains(text.stdout, " "+secret+" ") || strings.Contains(text.stdout, "Secret plans") ||
		!strings.Contains(text.stdout, "Private boards you aren't on (as an admin you see only that they exist):") ||
		!strings.Contains(text.stdout, " · created by maya on ") {
		t.Fatalf("the admin's list:\n%s", text)
	}
}

// Inside an agent's session, boards lists only that agent's board, with its own token.
func TestBoardsInAnAgentsSessionListsItsOwnBoard(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	first := tm.newBoard(maya, "private")
	tm.newBoard(maya, "open")
	tm.link(maya, first)
	s := maya.claudeSession("s-boards")
	s.run("join", field(t, maya.run("invite", "--json").json(t), "join_line").(string))
	r := s.e.exec(s.vars, "", "boards", "--json")
	if r.code != 0 {
		t.Fatalf("boards in a session:\n%s", r)
	}
	out := r.json(t)
	matchesCLISpec(t, "BoardsOutput", out)
	if out["as"] != "claude" || len(out["boards"].([]any)) != 1 || field(t, out, "boards.0.name") != first || field(t, out, "boards.0.role") != nil {
		t.Fatalf("an agent's boards: %v", out)
	}
	text := s.e.exec(s.vars, "", "boards")
	if !strings.HasPrefix(text.stdout, "Boards of agent claude on "+tm.url()+": an agent sees only its own board.\n") {
		t.Fatalf("an agent's boards, as text:\n%s", text)
	}
	if r := s.e.exec(s.vars, "", "boards", "--all", "--json"); r.code != 2 {
		t.Fatalf("--all in a session:\n%s", r)
	}
}

// ABOARD_AGENT alone, with no session and no --as, selects the agent: boards and board
// people read with its token, on its board only, and --all is refused.
func TestABoardAgentFromTheEnvironmentNeverUsesThePersonsLogin(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	first := tm.newBoard(maya, "private")
	second := tm.newBoard(maya, "private")
	tm.link(maya, first)
	maya.run("join", field(t, maya.run("invite", "--json").json(t), "join_line").(string), "--name", "scout")
	env := []string{"ABOARD_AGENT=scout"}

	r := maya.exec(env, "", "boards", "--json")
	if r.code != 0 {
		t.Fatalf("boards as ABOARD_AGENT:\n%s", r)
	}
	out := r.json(t)
	matchesCLISpec(t, "BoardsOutput", out)
	if out["as"] != "scout" || len(out["boards"].([]any)) != 1 || field(t, out, "boards.0.name") != first || strings.Contains(r.stdout, second) {
		t.Fatalf("boards as ABOARD_AGENT: %v", out)
	}
	if r := maya.exec(env, "", "boards", "--all", "--json"); r.code != 2 || errorCode(t, r.json(t)) != "invalid_request" {
		t.Fatalf("--all as ABOARD_AGENT:\n%s", r)
	}
	if r := maya.exec(env, "", "board", "people", "--board", second, "--json"); r.code != 1 || errorCode(t, r.json(t)) == "" {
		t.Fatalf("board people for another board as ABOARD_AGENT:\n%s", r)
	}
	people := maya.exec(env, "", "board", "people", "--json")
	if people.code != 0 || field(t, people.json(t), "board") != first {
		t.Fatalf("board people as ABOARD_AGENT:\n%s", people)
	}
}

// Alone on the local server, a person never sees the word open.
func TestBoardsForASoloUserShowNoVisibility(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "--title", "Docs review")
	board := field(t, e.run("status", "--json").json(t), "board").(string)
	expectLines(t, e.run("boards"),
		"Your boards on "+field(t, e.run("status", "--json").json(t), "server.url").(string)+":",
		"  "+board+` "Docs review" · owner · 1 person · 1 agent · default`)
}
