//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// guestKey returns the one key a guest's home saved, for the server its guest code named.
func guestKey(t *testing.T, e *env) string {
	t.Helper()
	var logins struct {
		Servers []struct{ Key string } `json:"servers"`
	}
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &logins); err != nil || len(logins.Servers) != 1 {
		t.Fatalf("the guest's servers.json: %s %v", raw, err)
	}
	return logins.Servers[0].Key
}

// The server's people are listed with their roles to every person on it; only an admin
// makes another admin, and the last admin stays one.
func TestServerPeopleAndRolesFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	tm.person("sam")

	out := tm.admin.run("people", "--json").json(t)
	matchesCLISpec(t, "PeopleOutput", out)
	if field(t, out, "people.0.server_role") != "admin" || field(t, out, "people.1.handle") != "maya" || field(t, out, "people.1.server_role") != "member" {
		t.Fatalf("people: %v", out)
	}
	expectLines(t, maya.run("people", "--server", tm.url()), "People on "+tm.url()+":", "  HANDLE  SERVER ROLE  NAME", "  @alex   admin", "  @maya   member", "  @sam    member")

	r := tm.admin.runExit("people", "role", "@alex", "member", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "last_admin" {
		t.Fatalf("the last admin made a member:\n%s", r)
	}
	r = maya.runExit("people", "role", "@maya", "admin", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_admin_required" {
		t.Fatalf("a member making herself an admin:\n%s", r)
	}
	role := tm.admin.run("people", "role", "@maya", "admin", "--json").json(t)
	matchesCLISpec(t, "PeopleRoleOutput", role)
	if role["changed"] != true || field(t, role, "person.server_role") != "admin" {
		t.Fatalf("maya made an admin: %v", role)
	}
	expectLines(t, tm.admin.run("people", "role", "maya", "admin"), "@maya is already an admin of "+tm.url()+".")
	if status, v := tm.call("PATCH", "/v1/people/maya", tm.admin.browserLogin(), map[string]any{"server_role": "member"}); status != http.StatusForbidden || errorCode(t, v) != "human_token_required" {
		t.Fatalf("an admin's browser changing a role: %d %v", status, v)
	}
	expectLines(t, maya.run("people", "role", "@alex", "member", "--server", tm.url()), "@alex is now a member of "+tm.url()+".")
	if r := tm.admin.runExit("people", "role", "@sam", "admin", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "server_admin_required" {
		t.Fatalf("alex, no longer an admin, changing a role:\n%s", r)
	}
}

// Removing a person from the server says first what stops and needs a yes; then their
// key stops working at once, their private board passes to the person on it longest,
// and their handle is free for a new person, who inherits nothing.
func TestRemovingAPersonFromTheServerFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	board := tm.newBoard(maya, "private")
	tm.link(maya, board)
	tm.link(sam, board)
	maya.run("board", "add", "@sam")
	mayaAgent := tm.agentToken(maya, board)

	r := tm.admin.runExit("people", "remove", "@maya", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" ||
		!strings.Contains(field(t, r.json(t), "error.message").(string), "Removing maya from "+tm.url()+" ends 1 key, 0 browser sessions and 1 agent. Ownership of 1 board passes to the person on it longest.") {
		t.Fatalf("removing without a yes:\n%s", r)
	}
	if status, _ := tm.call("GET", "/v1/me", tm.key(maya), nil); status != http.StatusOK {
		t.Fatalf("maya's key after the refused removal: %d", status)
	}
	if r := maya.runExit("people", "remove", "@sam", "--yes", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "server_admin_required" {
		t.Fatalf("a member removing someone:\n%s", r)
	}

	out := tm.admin.run("people", "remove", "@maya", "--yes", "--json").json(t)
	matchesCLISpec(t, "PeopleRemoveOutput", out)
	if field(t, out, "removal.keys_revoked") != float64(1) || field(t, out, "removal.agents_removed") != float64(1) || field(t, out, "removal.owners_passed") != float64(1) {
		t.Fatalf("the removal: %v", out)
	}
	for name, token := range map[string]string{"key": tm.key(maya), "agent": mayaAgent} {
		if status, v := tm.call("GET", "/v1/me", token, nil); status != http.StatusUnauthorized {
			t.Fatalf("maya's %s after removal: %d %v", name, status, v)
		}
	}
	r = maya.runExit("board", "people", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "unauthorized" {
		t.Fatalf("maya's CLI after removal:\n%s", r)
	}
	expectLines(t, sam.run("board", "people"), board+" · private · 1 person", "  sam (owner)")
	expectLines(t, tm.admin.run("people"), "People on "+tm.url()+":", "  HANDLE  SERVER ROLE  NAME", "  @alex   admin", "  @sam    member")

	// The handle is free: a new maya is a new person, who sees nothing of the old one's.
	again := tm.person("maya")
	if id := tm.me(tm.key(again))["id"]; id == "" || strings.Contains(out["removal"].(map[string]any)["person"].(map[string]any)["id"].(string), id.(string)) {
		t.Fatalf("the new maya's id: %v", id)
	}
	tm.link(again, board)
	if r := again.runExit("board", "people", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_not_found" {
		t.Fatalf("the new maya looking at the old maya's private board:\n%s", r)
	}
	sam.run("audit", "verify")
}

// A pairing code lets in only its maker's own sessions: another person's session can't
// redeem it, and is told how to get onto the board.
func TestAPairingCodeAdmitsOnlyItsMakersSessions(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	line := field(t, tm.admin.run("pair", "--json").json(t), "join.line").(string)
	board := field(t, tm.admin.run("status", "--json").json(t), "board").(string)
	s := maya.claudeSession("s-pairing")
	r := s.e.exec(s.vars, "", "join", line, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "join_code_not_yours" ||
		!strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard board add @maya --board "+board) {
		t.Fatalf("maya redeeming alex's pairing code:\n%s", r)
	}
	tm.admin.run("join", line, "--name", "helper")
}

// A guest code brings someone from outside the server onto one board: on a machine with
// no key for the server, their agent joins with the line alone, as a guest; the board's
// people show the guest; the guest's agent posts there and reaches nothing else; the code
// works once; and a person on the server can't use it.
func TestAGuestJoinsFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(maya, "private")
	other := tm.newBoard(tm.admin, "open")
	tm.link(maya, board)

	inv := maya.run("invite", "--guest", "sam", "--json").json(t)
	matchesCLISpec(t, "GuestInviteOutput", inv)
	line := inv["join_line"].(string)
	if !strings.HasPrefix(line, "Join Aboard board "+board+" on localhost:"+tm.admin.port()+" as guest with code ") || inv["guest"] != "sam" {
		t.Fatalf("the guest invite: %v", inv)
	}
	text := maya.run("invite", "--guest", "@lee")
	if !strings.HasPrefix(text.stdout, "Created a guest code for board "+board+" on "+tm.url()+": lee joins it as a guest from outside the server, once, within 24 hours. "+
		"Anyone with the code can use it, so give it only to lee.\n\nGive this to lee, to paste into their agent's session:\n\nJoin Aboard board "+board) ||
		!strings.HasSuffix(text.stdout, "\n"+invitePrompt+"\n") {
		t.Fatalf("the guest invite's text:\n%s", text)
	}

	if r := maya.runExit("join", line, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "guest_code_not_for_members" {
		t.Fatalf("maya, on the server, redeeming the guest code:\n%s", r)
	}

	guest := newPersonHome(t, "sam")
	s := guest.claudeSession("s-guest")
	joined := s.run("join", line, "--json").json(t)
	matchesCLISpec(t, "JoinOutput", joined)
	if joined["guest"] != true || field(t, joined, "agent.owner") != "sam" || field(t, joined, "server.url") != "http://localhost:"+tm.admin.port() {
		t.Fatalf("the guest's join: %v", joined)
	}
	again := newPersonHome(t, "sam")
	if r := again.claudeSession("s-again").e.exec(nil, "", "join", line, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "join_code_invalid" {
		t.Fatalf("the guest code used twice:\n%s", r)
	}

	// The guest's agent talks on the board, and its own daemon wakes it for maya's message.
	agent := field(t, joined, "agent.name").(string)
	s.run("say", "Hello from a guest.")
	status, v := tm.call("GET", "/v1/boards/"+board+"/messages", tm.key(maya), nil)
	if msgs, _ := v["messages"].([]any); status != http.StatusOK || len(msgs) != 1 || field(t, msgs[0], "body") != "Hello from a guest." {
		t.Fatalf("maya reading the guest's message: %d %v", status, v)
	}
	stop := s.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatalf("stop hook returned without a message\n%s", stop.wait(time.Second))
	}
	if status, v := tm.call("POST", "/v1/boards/"+board+"/messages", tm.key(maya), map[string]any{"body": "Welcome, sam.", "to": []string{"@" + agent}}); status != http.StatusCreated {
		t.Fatalf("maya posting to the guest's agent: %d %v", status, v)
	}
	woke := stop.wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `sender="other_person"`) || !strings.Contains(woke.stderr, "Welcome, sam.") {
		t.Fatalf("the guest's session should wake with maya's message\n%s", woke)
	}
	expectLines(t, maya.run("board", "people"), board+" · private · 2 people", "  maya (owner)", "  sam (guest)", "    @claude · claude-code · working")
	if got := field(t, maya.run("board", "people", "--json").json(t), "people.1.server_role"); got != "guest" {
		t.Fatalf("the guest's server role: %v", got)
	}
	expectLines(t, tm.admin.run("people"), "People on "+tm.url()+":", "  HANDLE  SERVER ROLE  NAME", "  @alex   admin", "  @maya   member", "  @sam    guest")

	// The guest's agent reaches its board and nothing else.
	if r := s.e.exec(s.vars, "", "board", "title", "Mine now", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "guest_not_allowed" {
		t.Fatalf("the guest's agent changing the title:\n%s", r)
	}
	boards := s.run("boards", "--json").json(t)
	if list := boards["boards"].([]any); len(list) != 1 || field(t, list[0], "name") != board {
		t.Fatalf("the guest's agent listing boards: %v", boards)
	}
	// The guest's key, saved on their machine, reaches the same board and nothing more.
	for _, token := range []string{guest.agentToken(board, agent), guestKey(t, guest)} {
		_, missing := tm.call("GET", "/v1/boards/no-such-board/messages", token, nil)
		status, v := tm.call("GET", "/v1/boards/"+other+"/messages", token, nil)
		if status != http.StatusNotFound || field(t, v, "error.message") != strings.ReplaceAll(field(t, missing, "error.message").(string), "no-such-board", other) {
			t.Fatalf("the guest reading %s: %d %v", other, status, v)
		}
	}
	if r := guest.runExit("people", "--server", "http://localhost:"+tm.admin.port(), "--json"); r.code != 1 || errorCode(t, r.json(t)) != "guest_not_allowed" {
		t.Fatalf("the guest listing the server's people:\n%s", r)
	}

	// A second guest code for sam, on another board, works with the key sam has now.
	tm.link(tm.admin, other)
	second := field(t, tm.admin.run("invite", "--guest", "sam", "--json").json(t), "join_line").(string)
	s2 := guest.claudeSession("s-guest-2")
	if again := s2.run("join", second, "--json").json(t); field(t, again, "board.name") != other || again["guest"] == true {
		t.Fatalf("sam's second guest code: %v", again)
	}
	boards = guest.run("boards", "--json").json(t)
	if list := boards["boards"].([]any); len(list) != 2 {
		t.Fatalf("sam's boards in a terminal: %v", boards)
	}
}
