//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

func TestAgentsListsOwnLocationsAndQuotesResumeCommands(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-find-agent")
	joined := s.run("join", "--board", board, "--server", tm.url(), "--json").json(t)
	name := field(t, joined, "agent.name").(string)
	token := tm.agentToken(tm.admin, board)
	folder := "/tmp/project's work; echo unsafe"
	sid := "conversation'; echo unsafe"
	status, _ := tm.call("PUT", "/v1/me/location", token, map[string]any{"harness": "claude-code", "session_id": sid, "folder": folder})
	if status != http.StatusOK {
		t.Fatalf("report location: %d", status)
	}
	for _, run := range []func(...string) result{tm.admin.run, s.run} {
		out := run("agents", "--server", tm.url(), "--json").json(t)
		matchesCLISpec(t, "AgentsOutput", out)
		groups := out["servers"].([]any)
		rows := groups[0].(map[string]any)["agents"].([]any)
		if len(rows) != 2 {
			t.Fatalf("own agents missing: %v", out)
		}
		found := false
		for _, raw := range rows {
			row := raw.(map[string]any)
			if !strings.Contains(row["resume_command"].(string), name) && row["location"] == nil {
				continue
			}
			if loc, ok := row["location"].(map[string]any); ok {
				if loc["folder"] != folder || loc["session_id"] != sid {
					t.Fatalf("location changed: %v", row)
				}
				command := row["reopen_command"].(string)
				if !strings.Contains(command, "'\"'\"'") || !strings.Contains(command, "--resume") {
					t.Fatalf("resume arguments not safely quoted: %s", command)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("reported location absent: %v", out)
		}
	}
}

func TestAgentsGroupsKnownIssuersAndKeepsSessionsOnTheirIssuer(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	person := first.person("sam")
	person.run("connect", second.invite())
	for _, url := range []string{first.url(), second.url()} {
		person.run("board", "new", "same", "--server", url, "--json")
		person.run("join", "--board", "same", "--server", url, "--json")
	}
	out := person.run("agents", "--json").json(t)
	matchesCLISpec(t, "AgentsOutput", out)
	groups := out["servers"].([]any)
	if len(groups) != 2 {
		t.Fatalf("issuer groups missing: %v", out)
	}
	for _, raw := range groups {
		g := raw.(map[string]any)
		rows := g["agents"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["board"] != "same" {
			t.Fatalf("wrong issuer's agents: %v", g)
		}
		if !strings.Contains(rows[0].(map[string]any)["resume_command"].(string), g["server"].(string)) {
			t.Fatalf("resume lost issuer: %v", g)
		}
	}
	s := person.claudeSession("s-find-issuer")
	s.run("join", "--board", "same", "--server", first.url(), "--json")
	result := s.run("agents", "--json").json(t)
	ownGroups := result["servers"].([]any)
	if len(ownGroups) != 1 || ownGroups[0].(map[string]any)["server"] != first.url() {
		t.Fatalf("session crossed issuers: %v", result)
	}
	refused := s.runExit("agents", "--server", second.url(), "--json")
	if refused.code == 0 {
		t.Fatalf("session used other issuer: %v", refused)
	}
}
