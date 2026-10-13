//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

func TestMessagesQuestionsRepliesAndAnswersReadLiteralTextFromStdin(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("stdin-messages")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	run := func(input string, vars []string, schema string, args ...string) map[string]any {
		t.Helper()
		r := tm.admin.exec(vars, input, append(args, "--json")...)
		if r.code != 0 {
			t.Fatalf("stdin command: %s", r)
		}
		data := r.json(t)
		matchesCLISpec(t, schema, data)
		return data
	}
	text := "Quotes: 'single' \"double\" `code` $HOME $(printf unsafe)\nUnicode: λ and café\n"
	posted := run(text, s.vars, "SayOutput", "say", "-", "--to", "@alex")
	if field(t, posted, "message.body") != text {
		t.Fatalf("literal message changed: %v", posted)
	}
	parent := tm.admin.run("say", "--board", board, "--to", "mine", "Please reply", "--json").json(t)
	replied := run(text, s.vars, "SayOutput", "say", "--reply", fmt.Sprint(field(t, parent, "message.seq")), "-")
	if field(t, replied, "message.body") != text {
		t.Fatalf("reply changed: %v", replied)
	}
	asked := run(text, s.vars, "AskOutput", "ask", "-", "Proceed", "Wait")
	if field(t, asked, "message.body") != text {
		t.Fatalf("question changed: %v", asked)
	}
	mentionText := "@alex stays question text"
	mentioned := run(mentionText, s.vars, "AskOutput", "ask", "-")
	if field(t, mentioned, "message.body") != mentionText {
		t.Fatalf("stdin question became a recipient: %v", mentioned)
	}
	answer := run(text, nil, "SayOutput", "say", "--board", board, "--reply", fmt.Sprint(field(t, asked, "message.seq")), "--option", "1", "-")
	if field(t, answer, "message.body") != text {
		t.Fatalf("answer changed: %v", answer)
	}
	options := run("Proceed with `code`", s.vars, "AskOutput", "ask", "Which option?", "-", "Wait")
	if field(t, options, "message.ask.options.0") != "Proceed with `code`" {
		t.Fatalf("option changed: %v", options)
	}
	going := run("Keep $value", s.vars, "AskOutput", "ask", "Can I continue?", "--going-with", "-", "--at", "20m")
	if field(t, going, "message.ask.going_with") != "Keep $value" {
		t.Fatalf("default changed: %v", going)
	}
	withdrawn := run(text, s.vars, "AskOutput", "ask", "--withdraw", fmt.Sprint(field(t, options, "message.seq")), "-")
	if field(t, withdrawn, "message.body") != text {
		t.Fatalf("withdrawal changed: %v", withdrawn)
	}
}

func TestTaskTextAndPairingWorkReadFromStdin(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("stdin-tasks")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	run := func(text, schema string, args ...string) map[string]any {
		t.Helper()
		r := tm.admin.exec(s.vars, text, append(args, "--json")...)
		if r.code != 0 {
			t.Fatalf("stdin command: %s", r)
		}
		data := r.json(t)
		matchesCLISpec(t, schema, data)
		return data
	}
	text := "Literal `code` and $value"
	created := run(text, "TaskOutput", "task", "new", "-")
	if field(t, created, "task.title") != text {
		t.Fatalf("title changed: %v", created)
	}
	ref := field(t, created, "task.ref").(string)
	about := run(text, "TaskOutput", "task", "new", "About check", "--about", "-", "--no-start")
	if field(t, about, "task.about.text") != text {
		t.Fatalf("about changed: %v", about)
	}
	note := run(text, "TaskOutput", "task", "note", "-", "--task", ref)
	if field(t, note, "task.stands.text") != text {
		t.Fatalf("note changed: %v", note)
	}
	dropText := "Drop reason: " + text
	dropped := run(dropText, "TaskOutput", "task", "drop", ref, "--reason", "-")
	if field(t, dropped, "task.owner") != nil {
		t.Fatalf("drop did not release owner: %v", dropped)
	}
	status, events := tm.call("GET", "/v1/boards/"+board+"/events?limit=200", tm.key(tm.admin), nil)
	if status != 200 {
		t.Fatalf("drop record: %d %v", status, events)
	}
	found := false
	for _, value := range field(t, events, "events").([]any) {
		event := value.(map[string]any)
		if event["type"] == "task.dropped" && field(t, event, "data.reason") == dropText {
			found = true
		}
	}
	if !found {
		t.Fatalf("drop reason did not reach the record: %v", events)
	}
	s.run("task", "start", ref)
	doneText := "Finished: " + text
	done := run(doneText, "TaskOutput", "task", "done", "-", "--task", ref)
	if field(t, done, "task.closed_note") != doneText {
		t.Fatalf("done note changed: %v", done)
	}
	pairing := run(text, "PairingOutput", "pairing", "request", "me", "-")
	if field(t, pairing, "request.work") != text {
		t.Fatalf("pairing work changed: %v", pairing)
	}
}

func TestBoardTitlesReadFromStdinAndBadTextDoesNotWrite(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	text := "Literal `code` and $value"
	r := e.exec(nil, text, "pair", "--title", "-", "--json")
	if r.code != 0 {
		t.Fatal(r)
	}
	if field(t, e.getAsOwner("/v1/boards/general"), "title") != text {
		t.Fatalf("pair title changed: %s", r)
	}
	r = e.exec(nil, text+" v2", "board", "title", "-", "--board", "general", "--json")
	if r.code != 0 || field(t, r.json(t), "after") != text+" v2" {
		t.Fatalf("board title: %s", r)
	}
	r = e.exec(nil, text, "board", "new", "stdin-board", "--title", "-", "--json")
	if r.code != 0 || field(t, r.json(t), "board.title") != text {
		t.Fatalf("new board title: %s", r)
	}
	before := e.getAsOwner("/v1/boards/general")
	for _, tc := range []struct {
		input, code string
		args        []string
		exit        int
	}{
		{"unused", "stdin_text_ambiguous", []string{"task", "new", "-", "--about", "-"}, 2},
		{string([]byte{0xff}), "stdin_text_invalid_utf8", []string{"say", "-", "--as", "member"}, 1},
		{strings.Repeat("a", 1024*1024+1), "stdin_text_too_large", []string{"say", "-", "--as", "member"}, 1},
		{"", "invalid_request", []string{"say", "-", "--as", "member"}, 2},
		{"GEN-1\n", "invalid_request", []string{"task", "note", "-"}, 2},
		{"GEN-1\n", "invalid_request", []string{"task", "done", "-"}, 2},
		{"GEN-1\n", "invalid_request", []string{"task", "drop", "--reason", "-"}, 2},
	} {
		args := append(append([]string{}, tc.args...), "--board", "general", "--json")
		r := e.exec(nil, tc.input, args...)
		if r.code != tc.exit || errorCode(t, r.json(t)) != tc.code {
			t.Fatalf("bad stdin: %s", r)
		}
	}
	after := e.getAsOwner("/v1/boards/general")
	if field(t, before, "head_seq") != field(t, after, "head_seq") {
		t.Fatal("bad stdin wrote to the board")
	}
}

func TestInvitePairingWorkReadsFromStdinAndStillNeedsApproval(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "private")
	s := tm.admin.claudeSession("stdin-invite-work")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	text := "Review `parse($input)` together\nKeep the next steps written down."
	r := tm.admin.exec(s.vars, text, "invite", "--person", "--board", board, "--server", tm.url(), "--pairing", "-", "--json")
	if r.code != 0 {
		t.Fatal(r)
	}
	held := r.json(t)
	if field(t, held, "approval.state") != "pending" {
		t.Fatalf("stdin bypassed invite approval: %v", held)
	}
	id := field(t, held, "approval.id").(string)
	tm.admin.run("approvals", "allow", id, "--server", tm.url(), "--json")
	collected := s.run("approvals", "show", id, "--board", board, "--server", tm.url(), "--json").json(t)
	matchesCLISpec(t, "ApprovalOutcomeOutput", collected)
	pairingID, ok := collected["pairing_request_id"].(string)
	if !ok {
		t.Fatalf("no pairing outcome after owner approval: %v", collected)
	}
	status, request := tm.call("GET", "/v1/pairing-requests/"+pairingID, tm.key(tm.admin), nil)
	if status != 200 || field(t, request, "work") != text {
		t.Fatalf("pairing work changed: %d %v", status, request)
	}
}
