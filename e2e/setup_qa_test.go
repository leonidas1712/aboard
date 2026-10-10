//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupEstablishesAnIssuerDefaultForTheNextSession(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := newPersonHome(t, "newcomer")
	session := person.claudeSession("setup-default")
	session.run("setup", tm.invite(), "--handle", "newcomer", "--json")
	servers := person.run("servers", "--json").json(t)
	if field(t, servers, "servers.0.default") != true {
		t.Fatalf("setup did not establish its issuing server as default: %v", servers)
	}
	nextSession := person.claudeSession("after-setup")
	listed := nextSession.run("pairing", "list", "--json").json(t)
	if field(t, listed, "server.url") != tm.url() {
		t.Fatalf("the next session selected a different issuer: %v", listed)
	}
}

func TestSetupKeepsTheMachinesExistingDefault(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	person := first.person("newcomer")
	person.run("servers", "use", first.url())
	session := person.claudeSession("setup-another-issuer")
	session.run("setup", second.invite(), "--handle", "newcomer", "--json")
	servers := person.run("servers", "--json").json(t)
	for _, item := range servers["servers"].([]any) {
		srv := item.(map[string]any)
		if srv["url"] == first.url() && srv["default"] == true {
			return
		}
	}
	t.Fatalf("setup replaced the existing default: %v", servers)
}

func TestPairedSetupKeepsHarnessInstructionsAndNewAccountMessage(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	sender := tm.admin.claudeSession("setup-inviter")
	joined := sender.run("join", "--board", board, "--server", tm.url(), "--json").json(t)
	status, selected := tm.call("GET", "/v1/boards/"+board, tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("read board: %d", status)
	}
	status, invited := tm.call("POST", "/v1/invites", tm.key(tm.admin), map[string]any{
		"boards":  []string{selected["id"].(string)},
		"pairing": map[string]any{"initiating_agent_id": field(t, joined, "agent.id"), "work": "Review together"},
	})
	if status != http.StatusCreated {
		t.Fatalf("issue paired invite: %d %v", status, invited)
	}
	person := newPersonHome(t, "newcomer")
	recipient := person.claudeSession("setup-recipient")
	out := recipient.run("setup", tm.url()+"/join#"+invited["invite"].(string), "--handle", "newcomer", "--json").json(t)
	if field(t, out, "steps.4.state") != "complete" || field(t, out, "steps.3.state") != "pending" {
		t.Fatalf("setup did not accept the endpoint while retaining pending harness confirmation: %v", out)
	}
	next := field(t, out, "next.resume").(string)
	if !strings.Contains(next, "/hooks") || !strings.Contains(next, "restart Claude Code") {
		t.Fatalf("pairing lost precise harness confirmation: %v", out)
	}
	if field(t, out, "steps.1.message") != "Your account was created and its saved key was verified." {
		t.Fatalf("new account was described as an existing pairing account: %v", out)
	}
}

func TestSetupKeepsAnInitializedLocalDefault(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := newPersonHome(t, "newcomer")
	person.run("up")
	// An initialized local server was the default before explicit defaults existed.
	raw, err := json.Marshal(map[string]any{"servers": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(person.configDir(), "servers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	session := person.claudeSession("setup-with-legacy-local")
	session.run("setup", tm.invite(), "--handle", "newcomer", "--json")
	listed := person.run("servers", "--json").json(t)
	for _, item := range listed["servers"].([]any) {
		srv := item.(map[string]any)
		if srv["local"] == true && srv["default"] == true {
			return
		}
	}
	t.Fatalf("setup moved the existing local default to the invitation issuer: %v", listed)
}
