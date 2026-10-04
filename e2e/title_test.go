//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// A board made with a title shows it beside its name; its admin changes or removes the
// title from a terminal, and the board's address stays its name throughout.
func TestBoardTitleFromPairAndBoardTitle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pair := e.run("pair", "--title", "Payments retry design")
	if !strings.Contains(pair.stdout, "Created board general (Payments retry design)") {
		t.Fatalf("pair doesn't show the title:\n%s", pair)
	}
	if got := field(t, e.getAsOwner("/v1/boards/general"), "title"); got != "Payments retry design" {
		t.Fatalf("title after pair = %v", got)
	}

	r := e.run("board", "title", "Retry", "design", "v2", "--json").json(t)
	if field(t, r, "board") != "general" || field(t, r, "before") != "Payments retry design" || field(t, r, "after") != "Retry design v2" {
		t.Fatalf("board title --json = %v", r)
	}
	if text := e.run("board", "title", ""); !strings.Contains(text.stdout, "Board general has no title now.") {
		t.Fatalf("removing the title:\n%s", text)
	}
	if got := field(t, e.getAsOwner("/v1/boards/general"), "title"); got != nil {
		t.Fatalf("title after removing = %v", got)
	}

	if r := e.runExit("board", "title", "--json"); r.code != 2 || field(t, r.json(t), "error.code") != "invalid_request" {
		t.Fatalf("board title without text should be a usage error\n%s", r)
	}
	long := strings.Repeat("x", 81)
	if r := e.runExit("board", "policy", "recommended", "--as", "writer", "--json"); r.code != 2 || field(t, r.json(t), "error.code") != "invalid_request" {
		t.Fatalf("board policy with --as should be a usage error: only a person changes the policy\n%s", r)
	}
	if r := e.runExit("board", "title", long, "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "invalid_request" {
		t.Fatalf("an 81-character title should be refused\n%s", r)
	}
}

// An agent sets its board's title from its session, for its owner, and the record names
// the agent; a new board an agent pairs can have a title too. The policy stays with the
// person: board policy is still refused in the session.
func TestAgentSetsTheBoardTitle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	claude := e.claudeSession("5f1c2d3e-0000-4000-8000-0000000000d1")
	claude.run("pair", "--name", "writer")

	r := claude.run("board", "title", "QA", "round", "--json").json(t)
	if field(t, r, "board") != "general" || field(t, r, "before") != nil || field(t, r, "after") != "QA round" {
		t.Fatalf("board title in a session = %v", r)
	}
	if text := claude.run("board", "title", "QA round 2"); !strings.Contains(text.stdout, `Board general is now titled "QA round 2".`) {
		t.Fatalf("board title text in a session:\n%s", text)
	}
	events := e.getAsOwner("/v1/boards/general/events")["events"].([]any)
	last := events[len(events)-1].(map[string]any)
	actor := last["actor"].(map[string]any)
	if last["type"] != "board.titled" || actor["kind"] != "agent" || actor["name"] != "writer" || actor["owner"] != "alex" {
		t.Fatalf("the record should show the agent set the title for alex: %v", last)
	}

	if r := claude.runExit("board", "policy", "recommended", "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" {
		t.Fatalf("board policy in a session should still be refused\n%s", r)
	}
	pair := claude.run("pair", "--new", "--title", "Second look")
	if !strings.Contains(pair.stdout, "(Second look)") {
		t.Fatalf("pair --new --title in a session:\n%s", pair)
	}
}
