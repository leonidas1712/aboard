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

func TestTaskCLIShowsSharedWorkAndReturnsDroppedParts(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	owner := tm.admin.claudeSession("s-task-owner")
	helper := tm.admin.claudeSession("s-task-helper")
	ownerSeat := owner.run("join", "--board", board, "--server", tm.url(), "--json").json(t)
	helperSeat := helper.run("join", "--board", board, "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "JoinOutput", ownerSeat)
	matchesCLISpec(t, "JoinOutput", helperSeat)
	ownerName := field(t, ownerSeat, "agent.name")
	helperName := field(t, helperSeat, "agent.name")
	if ownerName == helperName {
		t.Fatal("independent sessions unexpectedly share an agent seat")
	}
	created := owner.run("task", "new", "Check the shared lifecycle", "--no-start", "--about", "Keep the ownership visible", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", created)
	ref := field(t, created, "task.ref").(string)
	if field(t, created, "task.state") != "open" || field(t, created, "task.owner") != nil {
		t.Fatalf("unstarted task already has an owner: %v", created)
	}
	started := owner.run("task", "start", ref, "--json").json(t)
	matchesCLISpec(t, "TaskOutput", started)
	if field(t, started, "task.owner.name") != ownerName || field(t, started, "task.state") != "in_progress" {
		t.Fatalf("start did not assign the session's seat: %v", started)
	}
	joined := helper.run("task", "join", ref, "--json").json(t)
	matchesCLISpec(t, "TaskOutput", joined)
	if field(t, joined, "task.with.0.name") != helperName || field(t, joined, "task.owner.name") != ownerName {
		t.Fatalf("join did not keep the owner and add the helper: %v", joined)
	}
	firstNote := owner.run("task", "note", "Both paths found", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", firstNote)
	secondNote := owner.run("task", "note", "Both paths checked", "--task", ref, "--base", "1", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", secondNote)
	if field(t, secondNote, "task.stands.text") != "Both paths checked" || field(t, secondNote, "task.stands.version") != float64(2) {
		t.Fatalf("note did not replace the requested version: %v", secondNote)
	}
	listed := helper.run("task", "list", "--mine", "--all", "--limit", "1", "--json").json(t)
	matchesCLISpec(t, "TaskListOutput", listed)
	if field(t, listed, "tasks.0.ref") != ref {
		t.Fatalf("the helper's list lost its task: %v", listed)
	}
	shown := helper.run("task", "show", ref, "--json").json(t)
	matchesCLISpec(t, "TaskShowOutput", shown)
	if field(t, shown, "task.stands.text") != "Both paths checked" || field(t, shown, "task.with.0.name") != helperName {
		t.Fatalf("show did not expose current shared work: %v", shown)
	}
	dropped := helper.run("task", "drop", ref, "--reason", "Review finished", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", dropped)
	if field(t, dropped, "task.owner.name") != ownerName || len(field(t, dropped, "task.with").([]any)) != 0 || field(t, dropped, "line") != nil {
		t.Fatalf("helper drop changed the owner or retained helper work: %v", dropped)
	}
	returned := owner.run("task", "drop", "--reason", "Ready for someone else", "--json").json(t)
	matchesCLISpec(t, "TaskOutput", returned)
	if field(t, returned, "task.state") != "open" || field(t, returned, "task.owner") != nil || field(t, returned, "line") != nil {
		t.Fatalf("owner drop did not return the task and clear current work: %v", returned)
	}
	untagged := owner.run("say", "The work is available again", "--json").json(t)
	matchesCLISpec(t, "SayOutput", untagged)
	if len(field(t, untagged, "message.about").([]any)) != 0 {
		t.Fatalf("drop retained the agent's current task: %v", untagged)
	}
	verified := owner.run("audit", "verify", "--json").json(t)
	matchesCLISpec(t, "AuditVerifyOutput", verified)
}
