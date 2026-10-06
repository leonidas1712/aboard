//go:build e2e

package e2e

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type acceptanceSeat struct{ board, name, id, token string }

func acceptanceTwoSeats(t *testing.T) (*team, *env, *session, []acceptanceSeat) {
	t.Helper()
	tm := newTeam(t)
	maya := tm.person("maya")
	s := maya.claudeSession("s-public-multiseat")
	var seats []acceptanceSeat
	for range 2 {
		board := tm.newBoard(tm.admin, "open")
		out := s.run("join", "--board", board, "--json").json(t)
		seat := acceptanceSeat{board: board, name: field(t, out, "agent.name").(string), id: field(t, out, "agent.id").(string)}
		for _, c := range credentialsOf(t, maya) {
			if c["member_id"] == seat.id {
				seat.token = c["token"].(string)
			}
		}
		if seat.token == "" {
			t.Fatalf("no saved credential for seat %s", seat.id)
		}
		status, b := tm.call("PUT", "/v1/boards/"+board+"/members/"+seat.name+"/delivery", tm.key(maya), map[string]any{"mode": "off"})
		if status != http.StatusOK {
			t.Fatalf("set off: status %d, error %v", status, b["error"])
		}
		seats = append(seats, seat)
	}
	status := s.run("status", "--json").json(t)
	bound, ok := status["seats"].([]any)
	if !ok || len(bound) != 2 {
		t.Fatalf("second join did not retain both bound seats: seat count %d", len(bound))
	}
	for _, seat := range seats {
		found := false
		for _, raw := range bound {
			if raw.(map[string]any)["member_id"] == seat.id {
				found = true
			}
		}
		if !found {
			t.Fatalf("status omitted retained seat %s on %s", seat.id, seat.board)
		}
	}
	return tm, maya, s, seats
}

func acceptanceMessage(t *testing.T, tm *team, seat acceptanceSeat, body string) int {
	t.Helper()
	status, out := tm.call("POST", "/v1/boards/"+seat.board+"/messages", tm.key(tm.admin), map[string]any{"body": body, "to": []string{"@" + seat.name}})
	if status != http.StatusCreated {
		t.Fatalf("post: status %d, error %v", status, out["error"])
	}
	return int(out["seq"].(float64))
}

func acceptanceUnread(t *testing.T, tm *team, seat acceptanceSeat) (int, int) {
	t.Helper()
	status, out := tm.call("GET", "/v1/me/inbox", seat.token, nil)
	if status != http.StatusOK {
		t.Fatalf("inbox seat %s: status %d, error %v", seat.id, status, out["error"])
	}
	return len(out["messages"].([]any)), int(out["cursor"].(float64))
}

func TestPublicMultiseatEqualSequencesHaveSeparateAcknowledgementsAndReplies(t *testing.T) {
	tm, _, s, seats := acceptanceTwoSeats(t)
	seqA := acceptanceMessage(t, tm, seats[0], "question on first board")
	seqB := acceptanceMessage(t, tm, seats[1], "question on second board")
	if seqA != seqB {
		t.Fatalf("fixture needs equal sequences, got %d and %d", seqA, seqB)
	}
	nA, cA := acceptanceUnread(t, tm, seats[0])
	nB, cB := acceptanceUnread(t, tm, seats[1])
	if nA != 1 || nB != 1 {
		t.Fatalf("initial inbox counts %d, %d", nA, nB)
	}
	s.run("read", "--board", seats[0].board, "--json")
	if n, c := acceptanceUnread(t, tm, seats[0]); n != nA || c != cA {
		t.Fatal("timeline read advanced first cursor")
	}
	out := s.run("inbox", "--board", seats[0].board, "--json").json(t)
	if len(out["messages"].([]any)) != 1 {
		t.Fatal("explicit inbox omitted first message")
	}
	if n, c := acceptanceUnread(t, tm, seats[0]); n != 0 || c != seqA {
		t.Fatalf("first ack: unread %d cursor %d", n, c)
	}
	if n, c := acceptanceUnread(t, tm, seats[1]); n != nB || c != cB {
		t.Fatalf("first ack touched second board: unread %d cursor %d", n, c)
	}
	reply := s.run("say", "--board", seats[0].board, "--reply", strconv.Itoa(seqA), "--json", "reply from first seat").json(t)
	if field(t, reply, "message.board") != seats[0].board || field(t, reply, "message.reply_to_seq") != float64(seqA) {
		t.Fatal("explicit reply did not reference the first board's message")
	}
	all := s.run("inbox", "--json").json(t)
	matchesCLISpec(t, "InboxSeatsOutput", all)
	if len(all["seats"].([]any)) != 2 || all["unavailable"] != float64(0) {
		t.Fatal("aggregate inbox did not read both seats")
	}
	if n, c := acceptanceUnread(t, tm, seats[1]); n != 0 || c != seqB {
		t.Fatalf("second ack: unread %d cursor %d", n, c)
	}
}

func TestPublicMultiseatBoardAmbiguityPrecedesAgentSelection(t *testing.T) {
	tm, _, s, seats := acceptanceTwoSeats(t)
	acceptanceMessage(t, tm, seats[0], "first unread")
	acceptanceMessage(t, tm, seats[1], "second unread")
	heads := make([]any, len(seats))
	for i, seat := range seats {
		status, body := tm.call("GET", "/v1/boards/"+seat.board, tm.key(tm.admin), nil)
		if status != http.StatusOK {
			t.Fatalf("board head: status %d, error %v", status, body["error"])
		}
		heads[i] = body["head_seq"]
	}
	for _, selection := range []string{"none", "as", "env"} {
		for _, command := range []string{"read", "say"} {
			args := []string{command, "--json"}
			vars := append([]string{}, s.vars...)
			if selection == "as" {
				args = append(args, "--as", seats[0].name)
			}
			if selection == "env" {
				vars = append(vars, "ABOARD_AGENT="+seats[0].name)
			}
			if command == "say" {
				args = append(args, "ambiguous write must not happen")
			}
			r := s.e.exec(vars, "", args...)
			if r.code != 1 || errorCode(t, r.json(t)) != "board_ambiguous" {
				t.Fatalf("%s with %s: exit %d, expected board_ambiguous", command, selection, r.code)
			}
		}
	}
	for i, seat := range seats {
		if n, _ := acceptanceUnread(t, tm, seat); n != 1 {
			t.Fatalf("ambiguous command changed inbox of %s", seat.id)
		}
		status, body := tm.call("GET", "/v1/boards/"+seat.board, tm.key(tm.admin), nil)
		if status != http.StatusOK || body["head_seq"] != heads[i] {
			t.Fatalf("ambiguous command changed board %s: status %d, head %v", seat.board, status, body["head_seq"])
		}
	}
}

func TestPublicMultiseatOffBoardIsNotDeliveredWithAnotherBoardsWake(t *testing.T) {
	tm, maya, s, seats := acceptanceTwoSeats(t)
	status, out := tm.call("PUT", "/v1/boards/"+seats[0].board+"/members/"+seats[0].name+"/delivery", tm.key(maya), map[string]any{"mode": "focused"})
	if status != http.StatusOK {
		t.Fatalf("set focused: status %d, error %v", status, out["error"])
	}
	acceptanceMessage(t, tm, seats[1], "off board must stay unread")
	stop := s.startHook("stop")
	acceptanceMessage(t, tm, seats[0], "focused board wakes the session")
	woke := stop.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "focused board wakes the session") || strings.Contains(woke.stderr, "off board must stay unread") {
		t.Fatalf("focused wake violated off isolation: hook exit %d", woke.code)
	}
	if !strings.Contains(woke.stderr, "--board "+seats[0].board) {
		t.Fatal("multiseat delivery reply hint omitted board")
	}
	again := s.startHook("stop")
	eventually(t, 5*time.Second, "focused seat acknowledgement", func() bool { n, _ := acceptanceUnread(t, tm, seats[0]); return n == 0 })
	if prompt := s.hook("prompt", `"prompt":"continue"`); prompt.code != 0 {
		t.Fatalf("prompt exit %d", prompt.code)
	}
	if stopped := again.wait(5 * time.Second); stopped.code != 0 {
		t.Fatalf("released stop hook exit %d", stopped.code)
	}
	if n, _ := acceptanceUnread(t, tm, seats[1]); n != 1 {
		t.Fatalf("another board's wake acknowledged off traffic: unread %d", n)
	}
}

func TestPublicMultiseatRemovingOneSeatKeepsTheOtherUsable(t *testing.T) {
	tm, _, s, seats := acceptanceTwoSeats(t)
	status, out := tm.call("DELETE", "/v1/boards/"+seats[0].board+"/people/maya", tm.key(tm.admin), nil)
	if status != http.StatusOK {
		t.Fatalf("remove one seat: status %d, error %v", status, out["error"])
	}
	acceptanceMessage(t, tm, seats[1], "surviving seat still reads")
	s.run("inbox", "--board", seats[1].board, "--json")
	s.run("say", "--board", seats[1].board, "--to", "@alex", "surviving seat still posts")
	if n, _ := acceptanceUnread(t, tm, seats[1]); n != 0 {
		t.Fatalf("surviving seat not acknowledged: unread %d", n)
	}
	status, out = tm.call("GET", "/v1/boards/"+seats[0].board+"/messages", seats[0].token, nil)
	if status != http.StatusNotFound || errorCode(t, out) != "board_not_found" {
		t.Fatalf("removed seat can read: status %d, error %v", status, out["error"])
	}
	r := s.runExit("join", "--board", seats[0].board, "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "agent_removed" {
		t.Fatalf("removed seat rejoin: exit %d, expected agent_removed", r.code)
	}
}
