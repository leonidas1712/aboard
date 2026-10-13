//go:build e2e

package e2e

import (
	"reflect"
	"strings"
	"testing"
)

func TestTaskReferenceAsTextNeverChangesCurrentWork(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"note", "done", "drop"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.claudeSession("s-task-text")
			s.run("pair", "general")
			created := s.run("task", "new", "Keep this work", "--json").json(t)
			ref := field(t, created, "task.ref").(string)
			id := field(t, created, "task.id").(string)
			for _, text := range []string{ref, strings.ToLower(ref), id, "#3"} {
				before := s.run("task", "show", ref, "--json").json(t)
				args := []string{"task", op, text, "--json"}
				if op == "drop" {
					args = []string{"task", "drop", "--reason", text, "--json"}
				}
				r := s.e.exec(s.vars, "", args...)
				if r.code != 2 || errorCode(t, r.json(t)) != "invalid_request" {
					t.Fatalf("task ID used as text:\n%s", r)
				}
				err := r.json(t)["error"].(map[string]any)
				hint := err["hint"].(string)
				if text == "#3" {
					if !strings.Contains(err["message"].(string), "message reference") || strings.Contains(hint, "--task #3") {
						t.Fatalf("message reference treated as a task:\n%s", r)
					}
				} else if op == "drop" {
					if !strings.Contains(err["message"].(string), "not a reason") || !strings.Contains(hint, "aboard task drop "+text+" --reason") {
						t.Fatalf("drop hint doesn't match documented syntax:\n%s", r)
					}
				} else if !strings.Contains(hint, "--task "+text) {
					t.Fatalf("missing task selector hint:\n%s", r)
				}
				shown := s.run("task", "show", ref, "--json").json(t)
				if !reflect.DeepEqual(shown["task"], before["task"]) {
					t.Fatalf("refusal changed work: before %v, after %v", before, shown)
				}
				if got := field(t, s.run("say", "Still working", "--json").json(t), "message.about.0.ref"); got != ref {
					t.Fatalf("refusal cleared current work: %v", got)
				}
			}
			if op == "drop" {
				s.run("task", "drop", ref, "--reason", "Finished reviewing "+ref)
			} else {
				s.run("task", op, "Checked "+ref, "--task", ref)
			}
		})
	}
}

func TestTaskSelectorsTrimSpaceWithoutFallingBackToCurrentWork(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"note", "done", "drop"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			s := e.claudeSession("s-task-padding")
			s.run("pair", "general")
			created := s.run("task", "new", "Keep this work", "--json").json(t)
			ref := field(t, created, "task.ref").(string)
			for _, blank := range []string{"", " \t"} {
				args := []string{"task", op, "Checked", "--task", blank}
				if op == "drop" {
					args = []string{"task", "drop", blank}
				}
				r := s.e.exec(s.vars, "", append(args, "--json")...)
				if r.code != 2 || errorCode(t, r.json(t)) != "invalid_request" {
					t.Fatalf("blank task selector was not refused:\n%s", r)
				}
				if got := field(t, s.run("task", "show", ref, "--json").json(t), "task.state"); got != "in_progress" {
					t.Fatalf("blank selector changed current task: %v", got)
				}
			}
			args := []string{"task", op, "Checked", "--task", " \t" + ref + " "}
			if op == "drop" {
				args = []string{"task", "drop", " \t" + ref + " ", "--reason", "Checked"}
			}
			out := s.run(append(args, "--json")...).json(t)
			if field(t, out, "task.ref") != ref {
				t.Fatalf("padded selector picked another task: %v", out)
			}
		})
	}
}

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

func TestTaskDoneAndDropDoNotImmediatelyNudge(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"done", "drop"} {
		t.Run(op, func(t *testing.T) {
			tm := newTeam(t)
			board := tm.newBoard(tm.admin, "open")
			s := tm.admin.claudeSession("s-task-finish")
			s.run("join", "--board", board, "--server", tm.url())
			s.run("task", "new", "waiting work", "--no-start")
			s.run("task", "new", "finish this work")
			args := []string{"task", op, "--json"}
			if op == "done" {
				args = append(args, "Finished the work")
			}
			out := s.run(args...).json(t)
			if len(field(t, out, "nudges").([]any)) != 0 {
				t.Fatalf("finishing work immediately nudged: %v", out)
			}
		})
	}
}
