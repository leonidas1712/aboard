package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// An agent's reply to the person, addressed by the thread's default recipients, is
// pending until the person acknowledges past it, as the board view does, then read.
func TestAnAgentsReplyToThePersonIsReadOnceTheyAcknowledge(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, _ := s.pair("starter")
	ask := say(s, s.owner, boardName, []string{"@writer"}, "What's the plan?").JSON201
	r, err := s.client(writer).PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "Here it is.", ReplyTo: &ask.Id})
	mustStatus(t, r, err, 201)
	reply := r.JSON201
	all, got := s.receipts(writer, boardName, reply.Seq)
	if len(got) != 1 || all.Recipients[0].Member.Kind != "human" || all.Recipients[0].State != "pending" {
		t.Fatalf("receipts of the reply before the person read it: %+v", all.Recipients)
	}
	person := all.Recipients[0].Member.Name
	mustStatus(t, s.ackBoard(s.owner, boardName, reply.Seq), nil, 200)
	if _, got := s.receipts(writer, boardName, reply.Seq); got[person] != "read" {
		t.Fatalf("receipts after the person's ack: %v", got)
	}
	if _, got := s.receipts(s.owner, boardName, reply.Seq); got[person] != "read" {
		t.Fatalf("receipts after the person's ack, as the person: %v", got)
	}
}
