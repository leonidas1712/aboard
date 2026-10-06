package api_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/events"
)

func (s *testServer) react(token, messageID string, name api.ReactionName) *api.AddReactionResponse {
	s.t.Helper()
	r, err := s.client(token).AddReactionWithResponse(context.Background(), messageID, name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) unreact(token, messageID string, name api.ReactionName) *api.RemoveReactionResponse {
	s.t.Helper()
	r, err := s.client(token).RemoveReactionWithResponse(context.Background(), messageID, name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// eventsOf returns a board's events as token sees them, checking the chain verifies.
func (s *testServer) eventsOf(token, boardName string) ([]events.Event, *events.Verifier) {
	s.t.Helper()
	r, err := s.client(token).ListEventsWithResponse(context.Background(), boardName, nil)
	mustStatus(s.t, r, err, 200)
	var page struct {
		Events []events.Event `json:"events"`
	}
	if err := json.Unmarshal(r.Body, &page); err != nil {
		s.t.Fatal(err)
	}
	v := events.NewVerifier()
	if p := v.Add(page.Events); p != nil {
		s.t.Fatalf("chain problem %+v", p)
	}
	return page.Events, v
}

func reactionTypes(evs []events.Event) []string {
	var out []string
	for _, e := range evs {
		if e.Type == "reaction.added" || e.Type == "reaction.removed" {
			out = append(out, e.Type)
		}
	}
	return out
}

// Members react with emoji from the set and take them back; each change is one event in
// the record, a repeat changes nothing, and every reader sees who reacted.
func TestReactionsAreRecordedAndShownOnTheMessage(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	r := say(s, writer, boardName, []string{"@reviewer"}, "Draft is in notes.md.")
	mustStatus(t, r, nil, 201)
	msg := *r.JSON201
	if msg.Reactions == nil || len(msg.Reactions) != 0 {
		t.Fatalf("a new message's reactions = %#v, want an empty list", msg.Reactions)
	}

	a := s.react(reviewer, msg.Id, api.ReactionThumbsup)
	mustStatus(t, a, nil, 200)
	s.clock.Advance(time.Second)
	mustStatus(t, s.react(s.owner, msg.Id, api.ReactionThumbsup), nil, 200)
	mustStatus(t, s.react(reviewer, msg.Id, api.ReactionCheck), nil, 200)
	again := s.react(reviewer, msg.Id, api.ReactionCheck)
	mustStatus(t, again, nil, 200)

	want := []api.Reaction{
		{Name: api.ReactionThumbsup, Emoji: api.EmojiThumbsup, Count: 2, By: []string{"reviewer", "alex"}, Mine: true},
		{Name: api.ReactionCheck, Emoji: api.EmojiCheck, Count: 1, By: []string{"reviewer"}, Mine: true},
	}
	if !reactionsEqual(again.JSON200.Reactions, want) {
		t.Fatalf("the reviewer's view: %+v, want %+v", again.JSON200.Reactions, want)
	}
	// The writer reads them in the timeline, with mine false.
	l, err := s.client(writer).ListMessagesWithResponse(ctx, boardName, nil)
	mustStatus(t, l, err, 200)
	want[0].Mine, want[1].Mine = false, false
	if !reactionsEqual(l.JSON200.Messages[0].Reactions, want) {
		t.Fatalf("the writer's timeline: %+v, want %+v", l.JSON200.Messages[0].Reactions, want)
	}

	rm := s.unreact(reviewer, msg.Id, api.ReactionThumbsup)
	mustStatus(t, rm, nil, 200)
	mustStatus(t, s.unreact(reviewer, msg.Id, api.ReactionTada), nil, 200)
	want = []api.Reaction{
		{Name: api.ReactionThumbsup, Emoji: api.EmojiThumbsup, Count: 1, By: []string{"alex"}, Mine: false},
		{Name: api.ReactionCheck, Emoji: api.EmojiCheck, Count: 1, By: []string{"reviewer"}, Mine: true},
	}
	if !reactionsEqual(rm.JSON200.Reactions, want) {
		t.Fatalf("after taking 👍 back: %+v, want %+v", rm.JSON200.Reactions, want)
	}

	evs, _ := s.eventsOf(s.owner, boardName)
	if got := reactionTypes(evs); !slices.Equal(got, []string{"reaction.added", "reaction.added", "reaction.added", "reaction.removed"}) {
		t.Fatalf("reaction events: %v", got)
	}
	last := evs[len(evs)-1]
	var data struct {
		MessageID string `json:"message_id"`
		Name      string `json:"name"`
		Emoji     string `json:"emoji"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.MessageID != msg.Id || data.Name != "thumbsup" || data.Emoji != "👍" || last.Actor.Name == nil || *last.Actor.Name != "reviewer" {
		t.Fatalf("reaction.removed event: %s by %v", last.Data, last.Actor.Name)
	}
}

func reactionsEqual(a, b []api.Reaction) bool {
	return slices.EqualFunc(a, b, func(x, y api.Reaction) bool {
		return x.Name == y.Name && x.Emoji == y.Emoji && x.Count == y.Count && x.Mine == y.Mine && slices.Equal(x.By, y.By)
	})
}

// A reaction is not a message: it never reaches an inbox, never counts as unread and
// never ends an inbox wait.
func TestReactionsNeverReachAnInbox(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	r := say(s, writer, boardName, []string{"@reviewer"}, "Ready.")
	mustStatus(t, r, nil, 201)
	ack, err := s.client(reviewer).AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: r.JSON201.Seq})
	mustStatus(t, ack, err, 200)

	mustStatus(t, s.react(reviewer, r.JSON201.Id, api.ReactionEyes), nil, 200)
	mustStatus(t, s.react(s.owner, r.JSON201.Id, api.ReactionHeart), nil, 200)
	for _, token := range []string{writer, reviewer} {
		in, err := s.client(token).GetInboxWithResponse(ctx, nil)
		mustStatus(t, in, err, 200)
		if len(in.JSON200.Messages) != 0 {
			t.Fatalf("an inbox holds reactions: %s", in.Body)
		}
	}

	// A wait for the writer's inbox outlasts a reaction and ends with a real message.
	got := make(chan *api.GetInboxResponse, 1)
	wait := 30
	go func() {
		in, err := s.client(writer).GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait})
		if err != nil {
			t.Error(err)
		}
		got <- in
	}()
	mustStatus(t, s.react(reviewer, r.JSON201.Id, api.ReactionTada), nil, 200)
	reply := say(s, reviewer, boardName, []string{"@writer"}, "On it.")
	mustStatus(t, reply, nil, 201)
	select {
	case in := <-got:
		if in.StatusCode() != 200 || len(in.JSON200.Messages) != 1 || in.JSON200.Messages[0].Id != reply.JSON201.Id {
			t.Fatalf("the wait should end with the message alone: %s", in.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait didn't end when a message was posted")
	}
}

// A repeat with the same Idempotency-Key replays the answer.
func TestIdempotentReactionIsReplayed(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	r := say(s, writer, boardName, nil, "Shipped.")
	mustStatus(t, r, nil, 201)
	key := "react-1"
	c := s.client(reviewer)
	for range 2 {
		a, err := c.AddReactionWithResponse(ctx, r.JSON201.Id, api.ReactionTada, &api.AddReactionParams{IdempotencyKey: &key})
		mustStatus(t, a, err, 200)
	}
	d, err := c.RemoveReactionWithResponse(ctx, r.JSON201.Id, api.ReactionTada, &api.RemoveReactionParams{IdempotencyKey: &key})
	if code := errorCode(t, d, err, 422); code != "idempotency_conflict" {
		t.Fatalf("the same key for another request: %s", code)
	}
	evs, _ := s.eventsOf(s.owner, boardName)
	if got := reactionTypes(evs); len(got) != 1 {
		t.Fatalf("reaction events: %v, want one", got)
	}
}

// Under addressed visibility a member can react only to a message it may see, and the
// record withholds reactions on a message from a reader who may not see it.
func TestReactionsRespectVisibility(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	th := newThread(t, s) // under addressed visibility
	mustStatus(t, s.react(th.reviewer, th.root.Id, api.ReactionEyes), nil, 200)

	for _, id := range []string{th.root.Id, th.r1.Id, "msg_00000000000000000000000000"} {
		if code := errorCode(t, s.react(th.critic, id, api.ReactionEyes), nil, 404); code != "message_not_found" {
			t.Fatalf("the critic reacting to %s: %s", id, code)
		}
		if code := errorCode(t, s.unreact(th.critic, id, api.ReactionEyes), nil, 404); code != "message_not_found" {
			t.Fatalf("the critic taking back a reaction on %s: %s", id, code)
		}
	}
	// Someone not on the board can't react either.
	if code := errorCode(t, s.react(s.addHuman("blair"), th.root.Id, api.ReactionEyes), nil, 404); code != "message_not_found" {
		t.Fatalf("someone not on the board: %s", code)
	}
	// An unknown reaction is refused by name.
	if code := errorCode(t, s.react(th.reviewer, th.root.Id, "rocket"), nil, 400); code != "invalid_request" {
		t.Fatalf("an unknown reaction: %s", code)
	}

	evs, v := s.eventsOf(th.critic, th.board)
	for _, e := range evs {
		if e.Type == "reaction.added" && !e.DataWithheld {
			t.Fatalf("the critic sees a reaction on a message it may not see: %s", e.Data)
		}
	}
	// The root, r1, r2 and the reaction on the root are withheld from the critic.
	if v.Withheld != 4 {
		t.Fatalf("withheld %d events from the critic, want 4", v.Withheld)
	}
}

// The board's threads list newest activity first, with who wrote in each, counting only
// what the reader may see.
func TestThreadsListNewestActivityFirst(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	th := newThread(t, s)
	s.clock.Advance(time.Minute)
	late := reply(s, th.reviewer, th.board, th.other.Id, []string{"@writer"}, "Noted.")

	r, err := s.client(s.owner).ListThreadsWithResponse(ctx, th.board, nil)
	mustStatus(t, r, err, 200)
	got := r.JSON200
	if len(got.Threads) != 2 || got.More || got.Threads[0].Root.Id != th.other.Id || got.Threads[1].Root.Id != th.root.Id {
		t.Fatalf("the person's threads: %s", r.Body)
	}
	if !slices.Equal(got.Threads[0].Participants, []string{"writer", "reviewer"}) || got.Threads[0].Root.ReplyCount != 1 ||
		!got.Threads[0].Root.LastReplyAt.Equal(late.At) {
		t.Fatalf("the newest thread: %+v", got.Threads[0])
	}
	if !slices.Equal(got.Threads[1].Participants, []string{"writer", "reviewer", "critic"}) || got.Threads[1].Root.ReplyCount != 3 {
		t.Fatalf("the older thread: %+v", got.Threads[1])
	}

	one := 1
	l, err := s.client(s.owner).ListThreadsWithResponse(ctx, th.board, &api.ListThreadsParams{Limit: &one})
	mustStatus(t, l, err, 200)
	if len(l.JSON200.Threads) != 1 || !l.JSON200.More {
		t.Fatalf("limited to one: %s", l.Body)
	}

	// The reviewer may not see the critic's reply, so it isn't counted or named.
	v, err := s.client(th.reviewer).ListThreadsWithResponse(ctx, th.board, nil)
	mustStatus(t, v, err, 200)
	if len(v.JSON200.Threads) != 2 || !slices.Equal(v.JSON200.Threads[1].Participants, []string{"writer", "reviewer"}) ||
		v.JSON200.Threads[1].Root.ReplyCount != 2 {
		t.Fatalf("the reviewer's threads: %s", v.Body)
	}
	// The critic sees its own reply but not the root, so the thread isn't listed.
	c, err := s.client(th.critic).ListThreadsWithResponse(ctx, th.board, nil)
	mustStatus(t, c, err, 200)
	if len(c.JSON200.Threads) != 0 {
		t.Fatalf("the critic's threads: %s", c.Body)
	}
}
