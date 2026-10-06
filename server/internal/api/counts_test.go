package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// A board says how many messages it holds and when the last one came, to everyone who
// may read them all: its people, and its agents while visibility is open. Under addressed
// visibility an agent gets no count, which would tell it how much it can't read.
func TestBoardCountsItsMessages(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	human := s.client(s.owner)

	b, err := human.GetBoardWithResponse(ctx, boardName)
	mustStatus(t, b, err, 200)
	if b.JSON200.MessageCount == nil || *b.JSON200.MessageCount != 0 || b.JSON200.LastMessageAt != nil {
		t.Fatalf("new board: %s", b.Body)
	}
	say(s, writer, boardName, []string{"@reviewer"}, "one")
	s.clock.Advance(90 * time.Second)
	last := say(s, reviewer, boardName, []string{"@writer"}, "two")

	l, err := human.ListBoardsWithResponse(ctx, nil)
	mustStatus(t, l, err, 200)
	got := l.JSON200.Boards[0]
	if got.MessageCount == nil || *got.MessageCount != 2 || got.LastMessageAt == nil || !got.LastMessageAt.Equal(last.JSON201.At) {
		t.Fatalf("after two messages: %+v, last message at %v", got, last.JSON201.At)
	}
	open, err := s.client(writer).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, open, err, 200)
	if open.JSON200.MessageCount == nil || *open.JSON200.MessageCount != 2 {
		t.Fatalf("agent under open visibility: %s", open.Body)
	}

	preset := api.PolicyPreset("recommended")
	u, err := human.UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Policy: &api.PolicyChange{Preset: &preset}})
	mustStatus(t, u, err, 200)
	hidden, err := s.client(writer).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, hidden, err, 200)
	if hidden.JSON200.MessageCount != nil || hidden.JSON200.LastMessageAt != nil {
		t.Fatalf("agent under addressed visibility sees counts: %s", hidden.Body)
	}
	if u.JSON200.MessageCount == nil || *u.JSON200.MessageCount != 2 {
		t.Fatalf("person under addressed visibility: %s", u.Body)
	}
}
