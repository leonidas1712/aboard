//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetupJoinsAllRedeemedBoardsAndReusesItsSeats(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	first, second := tm.newBoard(tm.admin, "private"), tm.newBoard(tm.admin, "private")
	invite := tm.admin.run("invite", "--person", "--server", tm.url(), "--board", first, "--board", second, "--json").json(t)
	person := newPersonHome(t, "maya")
	session := person.claudeSession("paste-session-all-boards")
	out := session.run("setup", invite["link"].(string), "--handle", "maya", "--json").json(t)
	if field(t, out, "steps.4.state") != "complete" || field(t, out, "steps.5.state") != "pending" {
		t.Fatalf("setup must join without claiming a delivery round trip: %v", out)
	}
	status := session.run("status", "--json").json(t)
	seats, ok := status["seats"].([]any)
	if !ok || len(seats) != 2 {
		t.Fatalf("setup did not bind every redeemed board: %v", status)
	}
	ids := map[string]string{}
	for _, row := range seats {
		seat := row.(map[string]any)
		ids[seat["board"].(string)] = seat["member_id"].(string)
	}
	if ids[first] == "" || ids[second] == "" {
		t.Fatalf("wrong setup boards: %v", seats)
	}
	tokens := map[string]string{}
	for _, cred := range credentialsOf(t, person) {
		tokens[cred["member_id"].(string)] = cred["token"].(string)
	}
	session.run("setup", "--continue", "--json")
	for _, cred := range credentialsOf(t, person) {
		if tokens[cred["member_id"].(string)] != cred["token"] {
			t.Fatal("setup continuation rotated a current session seat token")
		}
	}
	repeated := session.run("status", "--json").json(t)
	for _, row := range repeated["seats"].([]any) {
		seat := row.(map[string]any)
		if ids[seat["board"].(string)] != seat["member_id"] {
			t.Fatalf("setup replaced a seat: %v", repeated)
		}
	}
	code, removed := tm.call("DELETE", "/v1/boards/"+first+"/people/maya", tm.key(tm.admin), nil)
	if code != http.StatusOK {
		t.Fatalf("remove membership: %d %v", code, removed)
	}
	text := session.run("setup", "--continue").stdout
	if strings.Contains(text, "pairing accept") || strings.Contains(text, "pairing select") || strings.Contains(text, "prq_") || !strings.Contains(text, "joining:") {
		t.Fatalf("setup exposed manual handshake details: %s", text)
	}
	code, people := tm.call("GET", "/v1/boards/"+first+"/people", tm.key(tm.admin), nil)
	if code != http.StatusOK {
		t.Fatalf("read people: %d %v", code, people)
	}
	for _, row := range people["people"].([]any) {
		if row.(map[string]any)["handle"] == "maya" {
			t.Fatalf("setup recreated removed membership: %v", people)
		}
	}
}

func TestSetupRetainsSeatsOnAnotherIssuer(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	person := first.person("maya")
	firstBoard, secondBoard := first.newBoard(person, "private"), second.newBoard(second.admin, "private")
	session := person.claudeSession("paste-session-two-issuers")
	joined := session.run("join", "--server", first.url(), "--board", firstBoard, "--json").json(t)
	invite := second.admin.run("invite", "--person", "--server", second.url(), "--board", secondBoard, "--json").json(t)
	session.run("setup", invite["link"].(string), "--handle", "maya", "--json")
	status := session.run("status", "--json").json(t)
	seats, ok := status["seats"].([]any)
	if !ok || len(seats) != 2 {
		t.Fatalf("setup did not retain both issuers: %v", status)
	}
	for _, row := range seats {
		seat := row.(map[string]any)
		if seat["server"] == first.url() && seat["member_id"] == field(t, joined, "agent.id") {
			return
		}
	}
	t.Fatalf("setup replaced the other issuer's seat: %v", status)
}
