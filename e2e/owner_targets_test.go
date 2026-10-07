//go:build e2e

package e2e

import (
	"testing"
)

func TestOwnerTargetsAndPersonOnlyMine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer := e.claudeSession("owner-writer")
	joined := writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t)
	line := field(t, joined, "join.line").(string)
	reviewer := e.claudeSession("owner-reviewer")
	reviewer.run("join", line, "--name", "reviewer")
	sent := e.run("say", "For my agents", "--to", "mine", "--board", "writer-reviewer", "--json").json(t)
	if field(t, sent, "message.from.kind") != "human" {
		t.Fatal(sent)
	}
	targets := field(t, sent, "message.to").([]any)
	if len(targets) != 1 || targets[0] != "owner:alex" {
		t.Fatal(targets)
	}
	for _, attempt := range []result{
		writer.runExit("say", "Forbidden", "--to", "mine", "--json"),
		e.exec([]string{"ABOARD_AGENT=writer"}, "", "say", "Forbidden", "--to", "mine", "--json"),
		e.runExit("say", "Forbidden", "--to", "mine", "--as", "writer", "--json"),
	} {
		if attempt.code != 1 || field(t, attempt.json(t), "error.code") != "human_command_in_session" {
			t.Fatal(attempt)
		}
	}
	agent := writer.run("say", "For my owner's agents", "--to", "owner:alex", "--json").json(t)
	if field(t, agent, "message.from.name") != "writer" {
		t.Fatal(agent)
	}
}
