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
	held := requester.run("invite", "--person", "--board", board, "--server", tm.url(), "--json").json(t)
	id := field(t, held, "approval.id").(string)
	page := mustSignIn(t, tm.url(), map[string]string{"key": tm.key(tm.admin)})
	status, approved := page.do(http.MethodPost, "/v1/me/approvals/"+id+"/allow", map[string]any{})
	if status != http.StatusOK || field(t, approved, "approval.state") != "executed" {
		t.Fatalf("browser approval: %d %v", status, approved)
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
	link := tm.url() + "/join#" + secret
	if prompt, ok := collected["prompt"].(string); !ok || !strings.Contains(prompt, link) {
		t.Fatalf("collected outcome lost its colleague prompt: %v", collected)
	}
	repeat := requester.run("approvals", "show", id, "--board", board, "--server", tm.url(), "--json")
	if out := repeat.json(t); out["collected"] != false || out["invite"] != nil || strings.Contains(repeat.stdout, secret) {
		t.Fatalf("repeat exposed or reissued the invite: %s", repeat)
	}
	colleague := newPersonHome(t, "colleague")
	colleague.run("connect", link, "--handle", "maya", "--json")
	status, people := tm.call(http.MethodGet, "/v1/boards/"+board+"/people", tm.key(colleague), nil)
	if status != http.StatusOK || len(people["people"].([]any)) != 2 {
		t.Fatalf("collected invite did not admit its colleague: %d %v", status, people)
	}
}
