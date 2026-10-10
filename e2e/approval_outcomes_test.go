//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestRequestingAgentCollectsBrowserApprovedInviteOnce(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	requester := tm.admin.claudeSession("approved-invite-requester")
	requester.run("join", "--board", board, "--server", tm.url(), "--json")
	held := requester.run("invite", "--person", "--board", board, "--server", tm.url(), "--pairing", "Review the proposed change", "--json").json(t)
	id := field(t, held, "approval.id").(string)
	if strings.Contains(field(t, held, "next.resume").(string), "pairing select") {
		t.Fatal("a held invite asked its agent to select an endpoint manually")
	}
	page := mustSignIn(t, tm.url(), map[string]string{"key": tm.key(tm.admin)})
	status, approved := page.do(http.MethodPost, "/v1/me/approvals/"+id+"/allow", map[string]any{})
	if status != http.StatusOK || field(t, approved, "approval.state") != "executed" {
		t.Fatalf("browser approval: %d %v", status, approved)
	}
	notice := requester.hook("prompt", `"prompt":"Continue the current task"`)
	if notice.code != 0 || !strings.Contains(notice.stdout, "aboard approvals show") || !strings.Contains(notice.stdout, id) || !strings.Contains(notice.stdout, board) || strings.Contains(notice.stdout, "abi_") || strings.Contains(notice.stdout, "pairing endpoint") {
		t.Fatalf("next-turn notice omitted collection or exposed a secret: %s", notice)
	}
	// The approving browser's secret is deliberately not handed to the requester.
	metadata := tm.admin.run("approvals", "show", id, "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "ApprovalOutcomeOutput", metadata)
	if metadata["collected"] != false || metadata["invite"] != nil {
		t.Fatalf("person read consumed or exposed the agent outcome: %v", metadata)
	}
	collected := requester.run("approvals", "show", id, "--board", board, "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "ApprovalOutcomeOutput", collected)
	if collected["collected"] != true || field(t, collected, "approval.state") != "executed" {
		t.Fatalf("requester could not collect its approved outcome: %v", collected)
	}
	secret := field(t, collected, "invite.invite").(string)
	pairingID := field(t, collected, "pairing_request_id").(string)
	status, pairing := tm.call(http.MethodGet, "/v1/pairing-requests/"+pairingID, tm.key(tm.admin), nil)
	if status != http.StatusOK || pairing["initiator"] == nil {
		t.Fatalf("the original requesting session was not selected: %d %v", status, pairing)
	}
	link := tm.url() + "/join#" + secret
	if field(t, collected, "invite.link") != link || field(t, collected, "invite.prompt") != collected["prompt"] {
		t.Fatalf("CLI changed the server-owned handover: %v", collected)
	}
	if prompt, ok := collected["prompt"].(string); !ok || !strings.Contains(prompt, link) {
		t.Fatalf("collected outcome lost its colleague prompt: %v", collected)
	}
	repeat := requester.run("approvals", "show", id, "--board", board, "--server", tm.url(), "--json")
	if out := repeat.json(t); out["collected"] != false || out["invite"] != nil || strings.Contains(repeat.stdout, secret) {
		t.Fatalf("repeat exposed or reissued the invite: %s", repeat)
	}
	if out := repeat.json(t); out["next"] == nil || !strings.Contains(field(t, out, "next.command").(string), "aboard invite revoke ") || !strings.Contains(field(t, out, "next.resume").(string), "Already collected") {
		t.Fatalf("repeat has no recovery command: %s", repeat)
	}
	if strings.Contains(collected["prompt"].(string), "Verify you can exchange") {
		t.Fatal("invite prompt repeats setup verification")
	}
	colleague := newPersonHome(t, "colleague")
	newcomer := colleague.claudeSession("approved-invite-colleague")
	setup := newcomer.run("setup", link, "--handle", "maya", "--json").json(t)
	if field(t, setup, "steps.1.state") != "complete" || field(t, setup, "steps.2.state") != "complete" {
		t.Fatalf("colleague setup did not preserve its account and membership: %v", setup)
	}
	status, people := tm.call(http.MethodGet, "/v1/boards/"+board+"/people", tm.key(colleague), nil)
	if status != http.StatusOK || len(people["people"].([]any)) != 2 {
		t.Fatalf("collected invite did not admit its colleague: %d %v", status, people)
	}
}
