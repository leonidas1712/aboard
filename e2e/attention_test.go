//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"
)

// An agent and its person ask whether a message reached its recipients: the agents of a
// role it was sent to are pending until their inbox takes it, then received; a person
// it named is pending until they mark it read; a message to everyone has no receipts.
func TestReceiptsFromTheCLI(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	people := e.run("board", "people", "--json").json(t)
	me := field(t, people, "people.0.name").(string)

	toRole := e.sayAs("writer", "--to", "role:reviewer", "Please both review.")
	r := e.run("read", "--receipts", fmt.Sprint(toRole), "--as", "writer", "--json").json(t)
	matchesCLISpec(t, "ReadReceiptsOutput", r)
	board := r["board"].(string)
	expectLines(t, e.run("read", "--receipts", fmt.Sprint(toRole), "--as", "writer"),
		fmt.Sprintf("%s · #%d to role:reviewer", board, toRole),
		"  reviewer  pending · no session now",
		"  critic    pending · no session now")

	// The reviewer's inbox takes it; reading the timeline doesn't, for the critic.
	e.run("inbox", "--as", "reviewer")
	e.run("read", "--as", "critic")
	expectLines(t, e.asAgent("writer", "read", "--receipts", fmt.Sprintf("#%d", toRole)),
		fmt.Sprintf("%s · #%d to role:reviewer", board, toRole),
		"  reviewer  received",
		"  critic    pending · no session now")

	// The person reads receipts with their own login, outside any agent's session.
	toMe := e.sayAs("writer", "--to", "@"+me, "A question for you.")
	expectLines(t, e.run("read", "--receipts", fmt.Sprint(toMe)), fmt.Sprintf("%s · #%d to @%s", board, toMe, me), "  "+me+"  pending")
	e.run("read", "--mark-read")
	expectLines(t, e.run("read", "--receipts", fmt.Sprint(toMe)), fmt.Sprintf("%s · #%d to @%s", board, toMe, me), "  "+me+"  read")

	all := e.sayAs("writer", "For everyone.")
	expectLines(t, e.run("read", "--receipts", fmt.Sprint(all), "--as", "writer"),
		fmt.Sprintf("%s · #%d to everyone · receipts are kept only for messages to someone", board, all))

	if r := e.runExit("read", "--receipts", "999", "--as", "writer", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "message_ref_invalid" {
		t.Fatalf("receipts of a message that isn't there:\n%s", r)
	}
	if r := e.runExit("read", "--receipts", fmt.Sprint(all), "--limit", "3"); r.code != 2 {
		t.Fatalf("--receipts with --limit:\n%s", r)
	}
}

// Each person's read position is theirs and kept by the server: what they haven't read
// is counted the same on every machine, aboard read --mark-read shows and marks only what
// it shows, and an agent's session can't mark its person's messages read.
func TestMarkReadKeepsEachPersonsPositionOnTheServer(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya, sam := tm.person("maya"), tm.person("sam")
	board := tm.newBoard(maya, "private")
	tm.link(maya, board)
	tm.link(sam, board)
	maya.run("board", "add", "@sam")
	samAgent := tm.agentToken(sam, board)

	post := func(token string, body map[string]any) int {
		t.Helper()
		status, v := tm.call("POST", "/v1/boards/"+board+"/messages", token, body)
		if status != http.StatusCreated {
			t.Fatalf("post: %d %v", status, v)
		}
		return int(v["seq"].(float64))
	}
	first := post(samAgent, map[string]any{"body": "one", "to": []string{"@maya"}})
	second := post(samAgent, map[string]any{"body": "two"})
	third := post(tm.key(sam), map[string]any{"body": "three"})

	boards := maya.run("boards", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", boards)
	if field(t, boards, "boards.0.unread") != float64(3) {
		t.Fatalf("maya's unread before reading: %v", boards)
	}
	// sam's own message isn't unread for him.
	if u := field(t, sam.run("boards", "--json").json(t), "boards.0.unread"); u != float64(2) {
		t.Fatalf("sam's unread: %v", u)
	}

	agent := field(t, tm.me(samAgent), "name").(string)
	expectLines(t, maya.run("read", "--mark-read", "--limit", "2"),
		board+" · 3 unread",
		fmt.Sprintf("#%d  @%s → @maya", first, agent), "    member · other_agent", "    one",
		fmt.Sprintf("#%d  @%s → all", second, agent), "    member · other_agent", "    two",
		fmt.Sprintf("Marked read up to #%d.", second),
		"1 more unread: aboard read --mark-read")
	r := maya.run("read", "--mark-read", "--limit", "2")
	if want := fmt.Sprintf("Marked read up to #%d.", third); r.lines()[len(r.lines())-1] != want {
		t.Fatalf("the second mark-read:\n%s", r)
	}
	if u := field(t, maya.run("boards", "--json").json(t), "boards.0.unread"); u != float64(0) {
		t.Fatalf("maya's unread after reading: %v", u)
	}
	out := maya.run("read", "--mark-read", "--json").json(t)
	matchesCLISpec(t, "ReadMarkOutput", out)
	if out["unread"] != float64(0) || len(out["messages"].([]any)) != 0 || out["read_up_to"] != float64(third) {
		t.Fatalf("mark-read with nothing unread: %v", out)
	}
	expectLines(t, maya.run("read", "--mark-read"), board+" · nothing unread")

	// sam sees maya read his agent's message to her.
	receipts := sam.run("read", "--receipts", fmt.Sprint(first), "--json").json(t)
	matchesCLISpec(t, "ReadReceiptsOutput", receipts)
	if field(t, receipts, "recipients.0.state") != "read" || field(t, receipts, "recipients.0.member.name") != "maya" {
		t.Fatalf("receipts of #%d: %v", first, receipts)
	}

	// Marking read is the person's own; inside an agent's session it is handed back.
	s := maya.claudeSession("s-mark")
	if r := s.e.exec(s.vars, "", "read", "--mark-read", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
		t.Fatalf("mark-read in an agent's session:\n%s", r)
	}
	if r := maya.runExit("read", "--mark-read", "--as", "codex"); r.code != 2 {
		t.Fatalf("mark-read with --as:\n%s", r)
	}
}
