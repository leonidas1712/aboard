//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestPairingCLIRequestListAndCancel(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-pairing-request")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	result := s.run("pairing", "request", "me", "--board", board, "Review the recorded change", "--json")
	for _, prefix := range []string{"abh_", "aba_", "abd_", "abp_"} {
		if strings.Contains(result.stdout, prefix) {
			t.Fatalf("pairing output contains a credential prefix %s", prefix)
		}
	}
	created := result.json(t)
	matchesCLISpec(t, "PairingOutput", created)
	if next, ok := created["next"].(map[string]any); !ok || !strings.Contains(next["command"].(string), tm.url()) {
		t.Fatalf("pairing handover does not retain the exact issuer: %v", created["next"])
	}
	id := field(t, created, "request.id").(string)
	listed := s.run("pairing", "list", "--json").json(t)
	matchesCLISpec(t, "PairingListOutput", listed)
	requests := field(t, listed, "requests").([]any)
	if len(requests) != 1 || requests[0].(map[string]any)["id"] != id {
		t.Fatalf("own request missing from pairing list: %v", listed)
	}
	cancelled := s.run("pairing", "cancel", id, "--json").json(t)
	matchesCLISpec(t, "PairingOutput", cancelled)
	if field(t, cancelled, "request.state") != "cancelled" {
		t.Fatalf("request was not cancelled: %v", cancelled)
	}
}

func TestPairingEndpointSelectionRequiresARealSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, action := range []string{"accept", "select"} {
		r := e.runExit("pairing", action, "prq_01K00000000000000000000000", "--here", "--json")
		if r.code != 1 || errorCode(t, r.json(t)) != "agent_session_required" {
			t.Fatalf("%s did not require an exact harness session: %v", action, r)
		}
	}
}

func TestPairingWrongSideAcceptanceDoesNotJoinAFreshSession(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.person("maya")
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	tm.admin.run("board", "add", "@maya", "--board", board, "--json")
	writer := tm.admin.claudeSession("s-pairing-wrong-side-writer")
	writer.run("join", "--board", board, "--server", tm.url(), "--json")
	created := writer.run("pairing", "request", "@maya", "--board", board, "Review the change", "--json").json(t)
	id := field(t, created, "request.id").(string)
	status, before := tm.call("GET", "/v1/boards/"+board+"/members", tm.key(tm.admin), nil)
	if status != 200 {
		t.Fatalf("list members before acceptance: %d", status)
	}
	fresh := tm.admin.claudeSession("s-pairing-wrong-side-fresh")
	r := fresh.runExit("pairing", "accept", id, "--here", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "forbidden" {
		t.Fatalf("the inviter accepted as the other person's recipient: %v", r)
	}
	status, after := tm.call("GET", "/v1/boards/"+board+"/members", tm.key(tm.admin), nil)
	if status != 200 || len(after["members"].([]any)) != len(before["members"].([]any)) {
		t.Fatalf("refused wrong-side acceptance joined a new seat: before=%v after=%v", before, after)
	}
}

func TestPairingCLIRequestHoldsNonmemberAdmission(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.person("maya")
	board := tm.newBoard(tm.admin, "private")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-pairing-held-admission")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	created := s.run("pairing", "request", "@maya", "--board", board, "Review the private change", "--json").json(t)
	matchesCLISpec(t, "PairingOutput", created)
	request := field(t, created, "request").(map[string]any)
	if request["state"] != "awaiting_endpoint" || request["initiator"] != nil || request["recipient"] != nil {
		t.Fatalf("held admission selected an endpoint: %v", created)
	}
	status, people := tm.call("GET", "/v1/boards/"+board+"/people", tm.key(tm.admin), nil)
	if status != 200 || len(people["people"].([]any)) != 1 {
		t.Fatalf("held admission added the recipient: status=%d people=%v", status, people)
	}
	status, approvals := tm.call("GET", "/v1/me/approvals", tm.key(tm.admin), nil)
	if status != 200 || len(approvals["approvals"].([]any)) != 1 {
		t.Fatalf("held admission lost its exact approval: status=%d approvals=%v", status, approvals)
	}
}

func TestUnboundInvitePrefersOwnPendingPairing(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	tm.admin.run("board", "add", "@maya", "--board", board, "--json")
	writer := tm.admin.claudeSession("s-invite-pairing-writer")
	writer.run("join", "--board", board, "--server", tm.url(), "--json")
	writer.run("pairing", "request", "@maya", "--board", board, "Review the change", "--json")
	fresh := maya.claudeSession("s-invite-pairing-recipient")
	r := fresh.runExit("invite", "--person", "--server", tm.url(), "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "agent_session_required" {
		t.Fatalf("unbound invitation did not refuse: %v", r)
	}
	command := field(t, r.json(t), "error.next.command").(string)
	if !strings.HasPrefix(command, "aboard pairing list --server ") || !strings.Contains(command, tm.url()) {
		t.Fatalf("pending own pairing was not offered on its issuer: %s", r)
	}
}
