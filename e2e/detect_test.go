//go:build e2e

package e2e

import (
	"slices"
	"strings"
	"testing"
)

// omp sets OMPCODE=1 and CLAUDECODE=1 in every command it runs, and a session started
// from a Claude Code session also inherits that session's ABOARD_SESSION until Aboard's
// extension in omp sets its own. A command inside omp is never taken for Claude Code: it
// doesn't act as the Claude Code session's agent, it refuses people's commands as an omp
// session's, and without the extension's session its agent comes from --as or
// ABOARD_AGENT.
func TestCommandsInsideOmpAreNotTakenForClaudeCode(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claude := e.claudeSession("5f1c2d3e-0000-4000-8000-0000000000aa")
	claude.run("pair", "writer-reviewer")
	if got := field(t, claude.run("status", "--json").json(t), "agent"); got != "claude" {
		t.Fatalf("in Claude Code the session's agent is %v, want claude", got)
	}
	omp := append(slices.Clone(claude.vars), "OMPCODE=1", "CLAUDECODE=1")

	r := e.exec(omp, "", "resume", "claude", "--json")
	if r.code == 0 || field(t, r.json(t), "error.code") != "session_unknown" {
		t.Fatalf("aboard resume inside omp:\n%s", r)
	}
	r = e.exec(omp, "", "say", "hello", "--json")
	if r.code == 0 || field(t, r.json(t), "error.code") != "agent_not_selected" {
		t.Fatalf("aboard say inside omp acted as some agent:\n%s", r)
	}
	r = e.exec(omp, "", "delivery", "off", "--as", "claude", "--json")
	msg, _ := field(t, r.json(t), "error.message").(string)
	if r.code == 0 || field(t, r.json(t), "error.code") != "human_command_in_session" ||
		strings.Contains(msg, "Claude Code") || !strings.Contains(msg, "inside an omp session") {
		t.Fatalf("aboard delivery off inside omp:\n%s", r)
	}
	if r := e.exec(omp, "", "say", "--as", "claude", "hello from omp", "--json"); r.code != 0 {
		t.Fatalf("aboard say --as inside omp:\n%s", r)
	}
}
