//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestStalePersonLoginNamesIssuerAndAgentKeepsRejoinHint(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := tm.person("maya")
	board := tm.newBoard(person, "private")
	list := tm.admin.run("keys", "--person", "maya", "--json").json(t)
	tm.admin.run("keys", "revoke", machineKeyName(t, list), "--person", "maya")
	for _, args := range [][]string{
		{"boards"}, {"status"}, {"status", "--board", board}, {"board", "policy", "recommended", "--board", board}, {"keys"}, {"invite", "--board", board},
	} {
		args = append(args, "--server", tm.url(), "--json")
		got := person.runExit(args...)
		out := got.json(t)
		if got.code != 1 || errorCode(t, out) != "unauthorized" {
			t.Fatalf("stale login didn't refuse %v: %s", args, got)
		}
		hint := field(t, out, "error.hint").(string)
		if !strings.Contains(hint, "aboard login "+tm.url()) || !strings.Contains(hint, "aboard servers use local") || strings.Contains(hint, "join") {
			t.Errorf("person hint %v: %q", args, hint)
		}
	}
	agent := tm.admin.claudeSession("agent-401-hint")
	ownBoard := tm.newBoard(tm.admin, "private")
	agent.run("join", "--board", ownBoard)
	tm.admin.run("board", "new", "other")
	// Revoke the key that issued the agent; its saved seat still selects the agent path.
	key := tm.admin.run("keys", "--json").json(t)
	tm.admin.run("keys", "revoke", field(t, key, "current_key_id").(string), "--yes")
	got := agent.runExit("read", "--json")
	out := got.json(t)
	hint := field(t, out, "error.hint").(string)
	if got.code != 1 || errorCode(t, out) != "unauthorized" || !strings.Contains(hint, "join") || strings.Contains(hint, "aboard login") {
		t.Fatalf("agent hint changed to person recovery: %s", got)
	}
}
