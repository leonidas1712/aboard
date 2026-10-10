//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestAgentRoleAndPolicyCommandsHoldExactApproval(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.person("maya")
	board := tm.newBoard(tm.admin, "private")
	session := tm.admin.claudeSession("admin-request")
	session.run("join", "--board", board, "--server", tm.url(), "--json")
	for _, test := range []struct {
		name string
		args []string
		kind string
	}{
		{"role", []string{"people", "role", "@maya", "admin", "--server", tm.url(), "--json"}, "set_server_role"},
		{"policy", []string{"board", "policy", "recommended", "--board", board, "--server", tm.url(), "--json"}, "set_board_policy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			held := session.run(test.args...).json(t)
			matchesCLISpec(t, "HeldAdminOutput", held)
			if held["state"] != "pending" || field(t, held, "approval.action.kind") != test.kind {
				t.Fatalf("action not held: %v", held)
			}
			id := field(t, held, "approval.id").(string)
			if next := field(t, held, "next.command").(string); !strings.Contains(next, "aboard approvals allow "+id) || !strings.Contains(next, tm.url()) {
				t.Fatalf("approval next lost exact id or issuer: %v", held)
			}
			if test.name == "role" {
				people := tm.admin.run("people", "--json").json(t)
				if field(t, people, "people.1.server_role") != "member" {
					t.Fatalf("role changed before approval: %v", people)
				}
			}
			refused := session.runExit("approvals", "allow", id, "--board", board, "--server", tm.url(), "--json")
			if refused.code != 1 || errorCode(t, refused.json(t)) != "human_token_required" {
				t.Fatalf("self approval: %s", refused)
			}
			always := tm.admin.runExit("approvals", "allow", id, "--always", "--json")
			if always.code != 1 || errorCode(t, always.json(t)) != "invalid_request" {
				t.Fatalf("risky action became allowable: %s", always)
			}
			approved := tm.admin.run("approvals", "allow", id, "--json").json(t)
			if field(t, approved, "approval.state") != "executed" {
				t.Fatalf("not executed: %v", approved)
			}
		})
	}
}

func TestAgentRoleRequestSelectsExplicitIssuerAcrossSeats(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	first.person("maya")
	second.person("maya")
	first.admin.run("connect", second.invite(), "--handle", "alex2", "--json")
	second.admin.run("people", "role", "@alex2", "admin", "--json")
	a := first.newBoard(first.admin, "private")
	b := second.newBoard(first.admin, "private")
	session := first.admin.claudeSession("two-issuer-admin")
	session.run("join", "--board", a, "--server", first.url(), "--name", "helper", "--json")
	session.run("join", "--board", b, "--server", second.url(), "--name", "helper", "--json")
	held := session.run("people", "role", "@maya", "admin", "--server", second.url(), "--json").json(t)
	if field(t, held, "server.url") != second.url() || held["state"] != "pending" {
		t.Fatalf("wrong issuer: %v", held)
	}
	firstApprovals := first.admin.run("approvals", "--server", first.url(), "--json").json(t)
	if len(firstApprovals["approvals"].([]any)) != 0 {
		t.Fatalf("foreign issuer got approval: %v", firstApprovals)
	}
	secondApprovals := first.admin.run("approvals", "--server", second.url(), "--json").json(t)
	if field(t, secondApprovals, "approvals.0.id") != field(t, held, "approval.id") {
		t.Fatalf("selected issuer lost request: %v", secondApprovals)
	}
}
