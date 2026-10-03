//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// invitePrompt is the sentence aboard invite puts under the join line.
const invitePrompt = "You have the Aboard skill. Join with this line, read the charter in the join output, then say hello on the board."

// A person adds an agent to a board that already exists: aboard invite prints a prompt
// whose join line brings a new agent onto that board, in the role the board's template
// invites.
func TestInviteAddsAnAgentToAnExistingBoard(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer")

	v := e.run("invite", "--json").json(t)
	matchesCLISpec(t, "InviteOutput", v)
	line := field(t, v, "join_line").(string)
	if field(t, v, "board") != "writer-reviewer" || field(t, v, "role") != "reviewer" {
		t.Fatalf("invite on writer-reviewer should invite a reviewer: %v", v)
	}
	if !strings.HasPrefix(line, "Join Aboard board writer-reviewer on localhost:"+e.port()+" as reviewer with code ") {
		t.Fatalf("join line %q", line)
	}
	if p := field(t, v, "prompt"); p != line+"\n"+invitePrompt {
		t.Fatalf("prompt %q", p)
	}

	// The text ends with the prompt alone, so a person can copy it whole.
	text := e.run("invite", "--role", "writer")
	lines := text.lines()
	if len(lines) < 3 || lines[len(lines)-1] != invitePrompt || lines[len(lines)-3] != "" ||
		!strings.Contains(lines[len(lines)-2], " as writer with code ") {
		t.Fatalf("invite text doesn't end with the prompt\n%s", text)
	}
	if !strings.Contains(text.stdout, "aboard board policy recommended") {
		t.Fatalf("invite on a starter board should say how to tighten it\n%s", text)
	}

	joined := e.run("join", line, "--name", "second-reviewer", "--json").json(t)
	if field(t, joined, "board.name") != "writer-reviewer" || field(t, joined, "agent.role") != "reviewer" {
		t.Fatalf("the invited agent joined %v as %v", field(t, joined, "board.name"), field(t, joined, "agent.role"))
	}
}

// Inviting is a person's action: inside a harness session aboard invite refuses and
// hands over the exact command, with the board named so it works from any directory.
func TestInviteRefusesInsideASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair")

	r := e.exec([]string{"CLAUDECODE=1"}, "", "invite", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("invite inside a session should refuse\n%s", r)
	}
	if hint := field(t, r.json(t), "error.hint").(string); !strings.HasSuffix(hint, ": aboard invite --board general") {
		t.Fatalf("hint should end with the command to hand over: %s", hint)
	}
	r = e.exec([]string{"CLAUDECODE=1"}, "", "invite", "--role", "member", "--json")
	if hint := field(t, r.json(t), "error.hint").(string); !strings.HasSuffix(hint, ": aboard invite --role member --board general") {
		t.Fatalf("hint should keep the role: %s", hint)
	}
}
