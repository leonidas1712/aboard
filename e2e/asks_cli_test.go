//go:build e2e

package e2e

import (
	"fmt"
	"testing"
)

func TestAskCLIRecordsDecisionAndUnblocksCurrentTask(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-ask-cli")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	created := s.run("task", "new", "Check recorded decisions", "--json").json(t)
	ref := field(t, created, "task.ref").(string)
	asked := s.run("ask", "Proceed with this change?", "Proceed", "Wait", "--json").json(t)
	matchesCLISpec(t, "AskOutput", asked)
	if field(t, asked, "message.ask.to.name") != "alex" || field(t, asked, "message.ask.task.ref") != ref || field(t, asked, "message.ask.blocking") != true {
		t.Fatalf("ask defaults: %v", asked)
	}
	seq := fmt.Sprint(field(t, asked, "message.seq"))
	shown := s.run("task", "show", ref, "--json").json(t)
	if field(t, shown, "task.blocked") != true {
		t.Fatal("ask did not block current task")
	}
	answer := tm.admin.run("say", "--board", board, "--reply", seq, "--option", "1", "--json").json(t)
	matchesCLISpec(t, "SayOutput", answer)
	if field(t, answer, "message.body") != "Proceed" || field(t, answer, "message.answer.option") != float64(1) {
		t.Fatalf("decision: %v", answer)
	}
	shown = s.run("task", "show", ref, "--json").json(t)
	if field(t, shown, "task.blocked") != false {
		t.Fatal("answered ask still blocks task")
	}
	s.run("audit", "verify", "--json")
}

func TestAskCLIListsWithdrawsAndDoesNotBlockGoingWith(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-ask-withdraw-cli")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	task := s.run("task", "new", "Check ask states", "--json").json(t)
	ref := field(t, task, "task.ref").(string)
	asked := s.run("ask", "Proceed?", "--json").json(t)
	open := s.run("ask", "--open", "--json").json(t)
	matchesCLISpec(t, "AskListOutput", open)
	if len(field(t, open, "asks").([]any)) != 1 {
		t.Fatalf("open asks: %v", open)
	}
	withdrawn := s.run("ask", "--withdraw", fmt.Sprint(field(t, asked, "message.seq")), "No longer needed", "--json").json(t)
	matchesCLISpec(t, "AskOutput", withdrawn)
	if field(t, withdrawn, "message.answer.withdrawn") != true {
		t.Fatalf("withdraw: %v", withdrawn)
	}
	going := s.run("ask", "Keep this change?", "--going-with", "Keep it", "--at", "20m", "--no-task", "--json").json(t)
	matchesCLISpec(t, "AskOutput", going)
	if field(t, going, "message.ask.blocking") != false || field(t, going, "message.ask.task") != nil {
		t.Fatalf("going-with: %v", going)
	}
	shown := s.run("task", "show", ref, "--json").json(t)
	if field(t, shown, "task.blocked") != false {
		t.Fatal("withdraw or going-with retained block")
	}
	s.run("audit", "verify", "--json")
}
