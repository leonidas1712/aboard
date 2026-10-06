package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// position returns the caller's read position and unread count on a board, as getBoard
// and listBoards report them, failing if the two disagree.
func (s *testServer) position(token, boardName string) (readUpTo, unread int) {
	s.t.Helper()
	ctx := context.Background()
	b, err := s.client(token).GetBoardWithResponse(ctx, boardName)
	mustStatus(s.t, b, err, 200)
	if b.JSON200.ReadUpTo == nil || b.JSON200.Unread == nil {
		s.t.Fatalf("no read position on %s: %s", boardName, b.Body)
	}
	l, err := s.client(token).ListBoardsWithResponse(ctx, nil)
	mustStatus(s.t, l, err, 200)
	for _, x := range l.JSON200.Boards {
		if x.Name == boardName && (x.ReadUpTo == nil || *x.ReadUpTo != *b.JSON200.ReadUpTo || *x.Unread != *b.JSON200.Unread) {
			s.t.Fatalf("the list and the board disagree: %v %v / %v %v", x.ReadUpTo, x.Unread, *b.JSON200.ReadUpTo, *b.JSON200.Unread)
		}
	}
	return *b.JSON200.ReadUpTo, *b.JSON200.Unread
}

func (s *testServer) ackBoard(token, boardName string, upTo int) *api.AckBoardResponse {
	s.t.Helper()
	r, err := s.client(token).AckBoardWithResponse(context.Background(), boardName, nil, api.AckBoardJSONRequestBody{UpTo: upTo})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

type unreadEvent struct {
	Board    string `json:"board"`
	ReadUpTo int    `json:"read_up_to"`
	Unread   int    `json:"unread"`
}

// unread returns the next unread event, failing if none comes in time.
func (st *eventStream) unread() unreadEvent {
	st.t.Helper()
	var b string
	select {
	case b = <-st.unreads:
	case <-st.done:
		st.t.Fatal("the stream ended")
	case <-time.After(streamWait):
		st.t.Fatal("no unread event arrived on the stream")
	}
	data, ok := strings.CutPrefix(b, "event: unread\ndata: ")
	if !ok {
		st.t.Fatalf("not an unread event: %q", b)
	}
	var u unreadEvent
	if err := json.Unmarshal([]byte(data), &u); err != nil {
		st.t.Fatalf("unread event data %q: %v", data, err)
	}
	return u
}

// A person's read position starts at the board's head and moves only when they
// acknowledge: reading the timeline, an earlier page or a thread leaves newer messages
// unread. It never moves back or past the head, their own messages never count, and
// every stream of theirs hears of each move.
func TestAPersonsReadPositionMovesOnlyWhenTheyAcknowledge(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	start, unread := s.position(s.owner, boardName)
	if unread != 0 {
		t.Fatalf("a new board starts with %d unread", unread)
	}
	if r, n := s.position(writer, boardName); n != 0 || r == 0 {
		t.Fatalf("the writer's position: %d, %d unread", r, n)
	}
	tab1, tab2 := s.openStream(s.owner), s.openStream(s.owner)
	if u := tab1.unread(); u.Board != boardName || u.ReadUpTo != start || u.Unread != 0 {
		t.Fatalf("the stream's first unread event: %+v", u)
	}
	tab2.unread()

	first := s.say(writer, boardName, "one")
	s.say(writer, boardName, "two")
	reply, err := s.client(writer).PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "three", ReplyTo: &first.Id, To: &[]api.Target{"all"}})
	mustStatus(t, reply, err, 201)
	mine := s.say(s.owner, boardName, "my own")
	if _, n := s.position(s.owner, boardName); n != 3 {
		t.Fatalf("unread after three messages and one of alex's: %d", n)
	}

	// Reading in every way moves nothing.
	newest, before := true, mine.Seq
	r, err := s.client(s.owner).ListMessagesWithResponse(ctx, boardName, &api.ListMessagesParams{Newest: &newest, Before: &before})
	mustStatus(t, r, err, 200)
	th, err := s.client(s.owner).ListRepliesWithResponse(ctx, first.Id, nil)
	mustStatus(t, th, err, 200)
	if got, n := s.position(s.owner, boardName); got != start || n != 3 {
		t.Fatalf("after reading: position %d with %d unread, want %d with 3", got, n, start)
	}

	ack := s.ackBoard(s.owner, boardName, first.Seq)
	mustStatus(t, ack, nil, 200)
	if ack.JSON200.ReadUpTo != first.Seq || ack.JSON200.Unread != 2 {
		t.Fatalf("ack up to #%d: %s", first.Seq, ack.Body)
	}
	for _, tab := range []*eventStream{tab1, tab2} {
		var u unreadEvent
		// A message posted while the stream is open is reported first.
		for u = tab.unread(); u.ReadUpTo != first.Seq; u = tab.unread() {
		}
		if u.Unread != 2 {
			t.Fatalf("unread event after the ack: %+v", u)
		}
	}
	if lower := s.ackBoard(s.owner, boardName, start); lower.JSON200.ReadUpTo != first.Seq {
		t.Fatalf("a lower ack moved the position back: %s", lower.Body)
	}
	if code := errorCode(t, s.ackBoard(s.owner, boardName, mine.Seq+50), nil, 422); code != "ack_out_of_range" {
		t.Fatalf("an ack past the head: %s", code)
	}
	if got := s.ackBoard(s.owner, boardName, mine.Seq); got.JSON200.Unread != 0 {
		t.Fatalf("ack to the end: %s", got.Body)
	}
}

// An agent's position on its board is its inbox's: the board's ack moves the same
// position as the inbox's, and its unread count is what its inbox holds.
func TestAnAgentsBoardAckIsItsInboxAck(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	m := s.say(writer, boardName, "for everyone")
	mustStatus(t, say(s, writer, boardName, []string{"@writer"}, "to myself"), nil, 201)
	if _, n := s.position(reviewer, boardName); n != 1 {
		t.Fatalf("the reviewer's unread: %d", n)
	}
	mustStatus(t, s.ackBoard(reviewer, boardName, m.Seq), nil, 200)
	in, err := s.client(reviewer).GetInboxWithResponse(ctx, nil)
	mustStatus(t, in, err, 200)
	if len(in.JSON200.Messages) != 0 || in.JSON200.Cursor != m.Seq {
		t.Fatalf("the inbox after the board's ack: %s", in.Body)
	}
}

// receipts reads a message's receipts as token, as a map from member name to state and
// presence.
func (s *testServer) receipts(token, boardName string, seq int) (all *api.Receipts, states map[string]string) {
	s.t.Helper()
	r, err := s.client(token).GetReceiptsWithResponse(context.Background(), boardName, seq)
	mustStatus(s.t, r, err, 200)
	out := map[string]string{}
	for _, rc := range r.JSON200.Recipients {
		v := string(rc.State)
		if rc.Presence != nil {
			v += " " + string(*rc.Presence)
		}
		out[rc.Member.Name] = v
	}
	return r.JSON200, out
}

// Receipts follow the recipients a message was addressed to when posted: an agent by
// name, the agents in a role then (not one who takes the role later), a person; never
// the sender, never anyone removed since. An agent's message is received once its read
// position passes it, a person's read once theirs does; a message to everyone has none.
func TestReceiptsFollowTheRecipientsFixedAtPosting(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	maya := s.addHuman("maya")
	add, err := s.client(s.owner).AddPersonWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: "maya"})
	mustStatus(t, add, err, 201)

	toWriter := say(s, s.owner, boardName, []string{"@writer"}, "for the writer").JSON201
	toRole := say(s, s.owner, boardName, []string{"role:reviewer"}, "for reviewers").JSON201
	toMaya := say(s, writer, boardName, []string{"@maya"}, "for maya").JSON201
	toAll := say(s, s.owner, boardName, nil, "for everyone").JSON201
	ownRole := say(s, reviewer, boardName, []string{"role:reviewer", "@writer"}, "my role and the writer").JSON201

	if _, got := s.receipts(s.owner, boardName, toWriter.Seq); len(got) != 1 || got["writer"] != "pending no_session" {
		t.Fatalf("receipts to @writer: %v", got)
	}
	in, err := s.client(writer).AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: toWriter.Seq})
	mustStatus(t, in, err, 200)
	if _, got := s.receipts(writer, boardName, toWriter.Seq); got["writer"] != "received no_session" {
		t.Fatalf("receipts after the writer's ack, as the writer: %v", got)
	}

	// A reviewer who joins later is never a recipient of what came before.
	role := "reviewer"
	late, err := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &boardName, Role: &role})
	mustStatus(t, late, err, 201)
	if _, got := s.receipts(s.owner, boardName, toRole.Seq); len(got) != 1 || got["reviewer"] != "pending no_session" {
		t.Fatalf("receipts to role:reviewer: %v", got)
	}
	if _, got := s.receipts(s.owner, boardName, ownRole.Seq); len(got) != 1 || got["writer"] != "pending no_session" {
		t.Fatalf("receipts of the reviewer's message to its own role and the writer: %v", got)
	}

	if _, got := s.receipts(maya, boardName, toMaya.Seq); got["maya"] != "pending" {
		t.Fatalf("receipts to @maya: %v", got)
	}
	mustStatus(t, s.ackBoard(maya, boardName, toMaya.Seq), nil, 200)
	if _, got := s.receipts(writer, boardName, toMaya.Seq); got["maya"] != "read" {
		t.Fatalf("receipts after maya's ack: %v", got)
	}

	all, got := s.receipts(s.owner, boardName, toAll.Seq)
	if !all.ToEveryone || len(got) != 0 {
		t.Fatalf("receipts of a message to everyone: %+v", all)
	}

	// The record keeps whom it was addressed to.
	ms, err := s.client(s.owner).ListMembersWithResponse(ctx, boardName)
	mustStatus(t, ms, err, 200)
	reviewerID := ""
	for _, m := range ms.JSON200.Members {
		if m.Name == "reviewer" {
			reviewerID = m.Id
		}
	}
	evs, _ := s.eventsOf(s.owner, boardName)
	for _, e := range evs {
		if e.Seq != int64(toRole.Seq) && e.Seq != int64(toAll.Seq) {
			continue
		}
		var d struct {
			Recipients *[]string `json:"recipients"`
		}
		if err := json.Unmarshal(e.Data, &d); err != nil {
			t.Fatal(err)
		}
		// Recipients are member ids: the reviewer's member id, not its name or person.
		want := e.Seq == int64(toRole.Seq)
		if want != (d.Recipients != nil && len(*d.Recipients) == 1 && (*d.Recipients)[0] == reviewerID) {
			t.Fatalf("recipients in the record of #%d: %s, want [%s]", e.Seq, e.Data, reviewerID)
		}
	}

	// Someone removed from the board drops out of the receipts.
	rm, err := s.client(s.owner).RemovePersonWithResponse(ctx, boardName, "maya", nil)
	mustStatus(t, rm, err, 200)
	if _, got := s.receipts(s.owner, boardName, toMaya.Seq); len(got) != 0 {
		t.Fatalf("receipts after maya's removal: %v", got)
	}
}

// Receipts go only to someone on the board who may read the message: an outsider of an
// open board is not_on_board, a message that isn't there or that an agent may not read
// under addressed visibility is message_not_found, and a private board is not found.
func TestReceiptsNeedTheBoardAndTheMessage(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("recommended")
	role := "member"
	third, err := s.client(s.owner).JoinWithResponse(ctx, nil, api.JoinRequest{Board: &boardName, Role: &role})
	mustStatus(t, third, err, 201)
	private := say(s, writer, boardName, []string{"@reviewer"}, "between us").JSON201

	if _, got := s.receipts(reviewer, boardName, private.Seq); got["reviewer"] != "pending no_session" {
		t.Fatalf("the recipient's view: %v", got)
	}
	r, err := s.client(third.JSON201.Token).GetReceiptsWithResponse(ctx, boardName, private.Seq)
	if code := errorCode(t, r, err, 404); code != "message_not_found" {
		t.Fatalf("a bystander under addressed visibility: %s", code)
	}
	r, err = s.client(writer).GetReceiptsWithResponse(ctx, boardName, private.Seq+100)
	if code := errorCode(t, r, err, 404); code != "message_not_found" {
		t.Fatalf("no such message: %s", code)
	}
	kim := s.addHuman("kim")
	r, err = s.client(kim).GetReceiptsWithResponse(ctx, boardName, private.Seq)
	if code := errorCode(t, r, err, 403); code != "not_on_board" {
		t.Fatalf("someone not on the open board: %s", code)
	}
	vis, err := s.client(s.owner).SetVisibilityWithResponse(ctx, boardName, nil, api.SetVisibilityRequest{Visibility: api.BoardVisibilityPrivate})
	mustStatus(t, vis, err, 200)
	r, err = s.client(kim).GetReceiptsWithResponse(ctx, boardName, private.Seq)
	if code := errorCode(t, r, err, 404); code != "board_not_found" {
		t.Fatalf("someone not on the private board: %s", code)
	}
	if code := errorCode(t, s.ackBoard(kim, boardName, 1), nil, 404); code != "board_not_found" {
		t.Fatalf("an ack on a private board kim isn't on: %s", code)
	}
}
