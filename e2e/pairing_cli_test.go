//go:build e2e

package e2e

import "testing"

func TestPairingCLIRequestListAndCancel(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-pairing-request")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	created := s.run("pairing", "request", "me", "--board", board, "Review the recorded change", "--json").json(t)
	matchesCLISpec(t, "PairingOutput", created)
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
