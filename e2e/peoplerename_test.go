//go:build e2e

package e2e

import "testing"

func TestRenamingAPersonKeepsTheirBoardAndAgents(t *testing.T) {
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(maya, "open")
	tm.link(maya, board)
	token := tm.agentToken(maya, board)
	status, before := tm.call("GET", "/v1/me", token, nil)
	if status != 200 {
		t.Fatalf("before: %d %v", status, before)
	}
	out := maya.run("people", "rename", "@maya", "sam", "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "PeopleRenameOutput", out)
	if field(t, out, "person.handle") != "sam" {
		t.Fatalf("rename: %v", out)
	}
	status, after := tm.call("GET", "/v1/me", token, nil)
	if status != 200 || field(t, after, "name") != field(t, before, "name") || field(t, after, "owner") != "sam" {
		t.Fatalf("seat changed: %d %v", status, after)
	}
	if got := maya.runExit("people", "rename", "sam", "alex", "--json"); got.code != 1 || errorCode(t, got.json(t)) != "handle_taken" {
		t.Fatalf("collision: %s", got)
	}
	if got := maya.runExit("people", "rename", "sam", "Bad_Handle", "--json"); got.code != 1 || errorCode(t, got.json(t)) != "invalid_request" {
		t.Fatalf("invalid: %s", got)
	}
}

func TestAgentCanRenameOwnPersonAndEditInviteSuggestion(t *testing.T) {
	tm := newTeam(t)
	e := tm.admin
	e.run("pair", "general", "--name", "writer")
	issued := e.run("invite", "--person", "--handle", "maya", "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "ServerInviteOutput", issued)
	listed := e.run("invite", "list", "--server", tm.url(), "--json").json(t)
	invites := listed["invites"].([]any)
	id := invites[0].(map[string]any)["id"].(string)
	edited := e.exec([]string{"ABOARD_AGENT=writer"}, "", "invite", "edit", id, "--handle", "sam", "--server", tm.url(), "--json")
	if edited.code != 0 {
		t.Fatalf("agent edit: %s", edited)
	}
	out := edited.json(t)
	matchesCLISpec(t, "ServerInviteEditOutput", out)
	if field(t, out, "invite.suggested_handle") != "sam" {
		t.Fatalf("edit: %v", out)
	}
	renamed := e.exec([]string{"ABOARD_AGENT=writer"}, "", "people", "rename", "alex", "leo", "--server", tm.url(), "--json")
	if renamed.code != 0 {
		t.Fatalf("agent rename: %s", renamed)
	}
	matchesCLISpec(t, "PeopleRenameOutput", renamed.json(t))
	if field(t, renamed.json(t), "person.handle") != "leo" {
		t.Fatal("own identity did not follow rename")
	}
	refused := e.exec([]string{"ABOARD_AGENT=missing"}, "", "people", "rename", "leo", "other", "--server", tm.url(), "--json")
	if refused.code == 0 {
		t.Fatal("unknown agent used person login")
	}
}
