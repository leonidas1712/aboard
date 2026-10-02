//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firstLine fails the test unless the command's first line of output is want.
func firstLine(t *testing.T, r result, want string) {
	t.Helper()
	if got := r.lines(); len(got) == 0 || got[0] != want {
		t.Fatalf("first line differs\nwant: %s\n\n%s", want, r)
	}
}

// Agents are named after the harness of the session that adds them, and after their
// role when added from a plain terminal.
func TestAgentsAreNamedAfterTheirHarness(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	first := e.claudeSession("s-first")
	pair := first.run("pair", "writer-reviewer")
	if !strings.Contains(pair.stdout, "Created board writer-reviewer and joined as claude (writer, owner alex)\n") {
		t.Fatalf("pair doesn't name the agent after Claude Code\n%s", pair)
	}
	line := pair.lines()[len(pair.lines())-1]

	firstLine(t, e.claudeSession("s-second").run("join", line), "Joined board writer-reviewer as claude-2 (reviewer, owner alex)")
	firstLine(t, e.codexSession("019a0000-0000-7000-8000-000000000002").run("join", line), "Joined board writer-reviewer as codex (reviewer, owner alex)")
	firstLine(t, e.run("join", line), "Joined board writer-reviewer as reviewer (owner alex)")
	firstLine(t, e.run("join", line, "--name", "critic"), "Joined board writer-reviewer as critic (reviewer, owner alex)")
}

// With harnesses hidden, new agents get neutral names and agents don't see which
// harness sent a message.
func TestHiddenHarnessesGiveNeutralNames(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	token, err := os.ReadFile(filepath.Join(e.home, ".config", "aboard", "local-owner-token"))
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.TrimSpace(string(token))
	if r := e.request("PATCH", "http://"+e.addr+"/v1/boards/writer-reviewer", owner, "",
		`{"policy":{"show_harness":false}}`); r.status != 200 {
		t.Fatalf("hiding harnesses: %d %s", r.status, r.body)
	}

	claude, codex := e.claudeSession("s-claude"), e.codexSession("019a0000-0000-7000-8000-000000000003")
	firstLine(t, claude.run("join", line), "Joined board writer-reviewer as agent-1 (reviewer, owner alex)")
	firstLine(t, codex.run("join", line), "Joined board writer-reviewer as agent-2 (reviewer, owner alex)")

	codex.run("say", "--to", "@agent-1", "hello")
	expectLines(t, e.run("inbox", "--as", "agent-1", "--peek"),
		"writer-reviewer · 1 new",
		`<aboard-message board="writer-reviewer" from="@agent-2" role="reviewer" sender="owner_agent" seq="8">`,
		"hello",
		"</aboard-message>",
	)
	expectLines(t, e.run("read", "--as", "agent-1"),
		"writer-reviewer · 1 message",
		"#8  @agent-2 (reviewer, owner_agent) → @agent-1",
		"    hello",
	)
	// People still see which harness each agent runs.
	if r := e.request("GET", "http://"+e.addr+"/v1/boards/writer-reviewer/messages", owner, "", ""); !strings.Contains(r.body, `"harness":"codex"`) {
		t.Fatalf("a person reading the board doesn't see the harness: %d %s", r.status, r.body)
	}
}
