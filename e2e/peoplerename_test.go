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
