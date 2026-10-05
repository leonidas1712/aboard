package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// A command that acts on one board, in a session with several seats, needs --board; it
// names the seats sorted by board. With one seat or none, or with --board, nothing is
// refused.
func TestBoardAmbiguous(t *testing.T) {
	seats := []delivery.AgentRef{
		{Server: "https://team.example.com", Board: "payments-design", Name: "claude-2"},
		{Server: "https://team.example.com", Board: "general", Name: "claude"},
	}
	for _, ok := range []struct {
		seats []delivery.AgentRef
		board string
	}{{nil, ""}, {seats[:1], ""}, {seats, "general"}} {
		if err := boardAmbiguous(ok.seats, ok.board); err != nil {
			t.Errorf("%v with --board %q: %v", ok.seats, ok.board, err)
		}
	}
	err := boardAmbiguous(seats, "")
	var e *Error
	if !errors.As(err, &e) || e.Code != "board_ambiguous" {
		t.Fatalf("several seats: %v", err)
	}
	if e.Message != "This session has seats on 2 boards: general, payments-design." ||
		e.Hint != "Pass --board with one of them, such as aboard say --board general \"…\"." {
		t.Errorf("text: %q / %q", e.Message, e.Hint)
	}
	want := map[string]any{
		"boards": []string{"general", "payments-design"},
		"seats":  []map[string]string{{"board": "general", "name": "claude"}, {"board": "payments-design", "name": "claude-2"}},
	}
	if !reflect.DeepEqual(e.Details, want) {
		t.Errorf("details: %v", e.Details)
	}
}

// A join made with a person's key from a session sends the session only in the form
// the API takes.
func TestSessionParam(t *testing.T) {
	key := delivery.SessionKey{Harness: "claude-code", ID: "5f1c2d3e-0000-4000-8000-000000000001"}
	if got := sessionParam(key, true); got == nil || *got != "claude-code:5f1c2d3e-0000-4000-8000-000000000001" {
		t.Errorf("in a session: %v", got)
	}
	if got := sessionParam(key, false); got != nil {
		t.Errorf("outside a session: %v", *got)
	}
	if got := sessionParam(delivery.SessionKey{Harness: "codex", ID: "has a space"}, true); got != nil {
		t.Errorf("an id the API refuses: %v", *got)
	}
}

// In a session, boards lists each board with its agents and the session's place on it.
func TestSessionBoardsText(t *testing.T) {
	three, none := 3, 0
	name := "claude"
	rows := []boardsRow{
		{Name: "payments-design", Visibility: "open", OnBoard: true, Agents: &three, Seat: &seatName{&name}},
		{Name: "incident-42", Visibility: "private", OnBoard: true, Agents: &none, Seat: &seatName{}},
		{Name: "lobby", Visibility: "open", Seat: &seatName{}},
	}
	want := "payments-design   open      3 agents   you're claude here\n" +
		"incident-42       private   you're on it\n" +
		"lobby             open      join with aboard join --board lobby\n"
	if got := sessionBoardsText(rows); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// With several seats, status lists one line per seat, sorted by board, and the two
// reminders that every command acting on one board needs --board.
func TestSeatsText(t *testing.T) {
	member, idle, two := "member", "idle", 2
	rows := []seatRow{
		{Board: "general", Name: "claude", Role: &member, Delivery: "focused", Presence: &idle, Unread: &two},
		{Board: "payments-design", Name: "claude-2", Role: &member, Delivery: "all", Presence: &idle},
	}
	text, usage := seatsText("https://team.example.com", rows)
	want := "Seats:  2 in this session, on https://team.example.com\n" +
		"          general          claude    member  focused  idle  2 unread\n" +
		"          payments-design  claude-2  member  all      idle\n" +
		"        Commands that act on one board need --board, such as aboard say --board general \"…\".\n" +
		"        Reply with: aboard say --board payments-design --reply SEQ \"…\"\n"
	if text != want {
		t.Errorf("got:\n%s\nwant:\n%s", text, want)
	}
	if len(usage) != 2 {
		t.Errorf("usage: %v", usage)
	}
}
