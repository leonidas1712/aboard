//go:build e2e

package e2e

import (
	"testing"
)

func TestTasksTagMessagesAndClearCurrentWorkFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-task-cli")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	created := s.run("task", "new", "Check the task flow", "--about", "Prove CLI tagging", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", created)
	ref := field(t, created, "task.ref").(string)
	posted := s.run("say", "Working on the check", "--json").json(t)
	matchesCLISpec(t, "SayOutput", posted)
	if field(t, posted, "message.about.0.ref") != ref {
		t.Fatalf("current task not attached to message: %v", posted)
	}
	page := s.run("read", "--task", ref, "--json").json(t)
	matchesCLISpec(t, "ReadOutput", page)
	done := s.run("task", "done", "The flow works", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", done)
	if field(t, done, "task.state") != "done" {
		t.Fatalf("task was not finished: %v", done)
	}
	s.run("audit", "verify", "--json")
}
