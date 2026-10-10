//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestAllowanceAndExactInviteApprovalFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	session := tm.admin.claudeSession("allowance-owner")
	session.run("join", "--board", board, "--server", tm.url(), "--json")
	initial := tm.admin.run("allowance", "--json").json(t)
	matchesCLISpec(t, "AllowanceOutput", initial)
	if len(field(t, initial, "allowance.categories").([]any)) != 0 {
		t.Fatal("allowance did not start off")
	}
	held := session.run("invite", "--person", "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "HeldAdminOutput", held)
	id := field(t, held, "approval.id").(string)
	if held["state"] != "pending" || !strings.Contains(field(t, held, "next.command").(string), tm.url()) {
		t.Fatalf("held command lost issuer or approval: %v", held)
	}
	listed := session.run("approvals", "--board", board, "--json").json(t)
	matchesCLISpec(t, "ApprovalsOutput", listed)
	if field(t, listed, "approvals.0.id") != id {
		t.Fatalf("agent's inventory: %v", listed)
	}
	refused := session.runExit("approvals", "allow", id, "--board", board, "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "human_token_required" || !strings.Contains(field(t, refused.json(t), "error.next.command").(string), id) {
		t.Fatalf("agent approved itself or lost handover: %s", refused)
	}
	allowed := tm.admin.run("approvals", "allow", id, "--always", "--json").json(t)
	matchesCLISpec(t, "ApprovalDecisionOutput", allowed)
	if warning, ok := allowed["warning"].(string); !ok || !strings.Contains(warning, "every open board") {
		t.Fatalf("always invite approval has no warning: %v", allowed)
	}
	alwaysText := tm.admin.run("approvals", "allow", id, "--always")
	if !strings.Contains(alwaysText.stdout, "every open board") {
		t.Fatalf("always invite approval text has no warning: %s", alwaysText)
	}
	if field(t, allowed, "approval.state") != "executed" {
		t.Fatalf("approval: %v", allowed)
	}
	link := tm.url() + "/join#" + field(t, allowed, "invite.invite").(string)
	newPersonHome(t, "invitee").run("connect", link, "--json")
	repeated := tm.admin.run("approvals", "allow", id, "--json").json(t)
	if _, warned := repeated["warning"]; warned {
		t.Fatal("plain allow warned about enabling an allowance")
	}
	if _, present := repeated["invite"]; present {
		t.Fatal("executed approval returned a secret twice")
	}
	categories := tm.admin.run("allowance", "--json").json(t)
	if field(t, categories, "allowance.categories.0") != "invite-people" {
		t.Fatalf("always category: %v", categories)
	}
	automatic := session.run("invite", "--person", "--server", tm.url(), "--json").json(t)
	if field(t, automatic, "approval.state") != "executed" || field(t, automatic, "approval.execution.authorization.via") != "allowance" {
		t.Fatalf("automatic admission: %v", automatic)
	}
	inventory := tm.admin.run("invite", "list")
	if !strings.Contains(inventory.stdout, "issued by agent ") {
		t.Fatalf("agent-issued invites not identified: %s", inventory)
	}
	tm.admin.run("allowance", "set", "invite-people", "off", "--json")
	pending := session.run("invite", "--person", "--server", tm.url(), "--json").json(t)
	pendingID := field(t, pending, "approval.id").(string)
	declined := tm.admin.run("approvals", "decline", pendingID, "--json").json(t)
	matchesCLISpec(t, "ApprovalDecisionOutput", declined)
	if field(t, declined, "approval.state") != "declined" {
		t.Fatalf("decline: %v", declined)
	}
	on := tm.admin.run("allowance", "on", "--json").json(t)
	if categories := field(t, on, "allowance.categories").([]any); len(categories) != 1 || categories[0] != "add-people" {
		t.Fatalf("main switch permitted invites: %v", on)
	}
	explicit := tm.admin.run("allowance", "set", "invite-people", "on", "--json").json(t)
	if !strings.Contains(field(t, explicit, "allowance.warning").(string), "every open board") {
		t.Fatalf("explicit opt-in has no warning: %v", explicit)
	}
	text := tm.admin.run("allowance", "set", "invite-people", "on")
	if !strings.Contains(text.stdout, "every open board") {
		t.Fatalf("text opt-in has no warning: %s", text)
	}
	tm.admin.run("allowance", "set", "add-people", "off", "--json")
	tm.admin.run("allowance", "off", "--json")
	matchesCLISpec(t, "ApprovalsOutput", tm.admin.run("approvals", "--json").json(t))
}
