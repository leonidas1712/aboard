package api_test

import (
	"context"
	"slices"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// replyDefault posts body as token in reply to the message with id to, naming no one.
func replyDefault(s *testServer, token, boardName, to, body string) *api.PostMessageResponse {
	s.t.Helper()
	r, err := s.client(token).PostMessageWithResponse(context.Background(), boardName, nil,
		api.PostMessageRequest{Body: body, ReplyTo: &to})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func targets(m api.Message) []string {
	out := make([]string, 0, len(m.To))
	for _, t := range m.To {
		out = append(out, string(t))
	}
	return out
}

// A reply that names no one goes to the people and agents already in its thread that the
// sender may see, the author of the message it answers first, never to everyone; an
// explicit target overrides it, and a thread with no one else in it is refused.
func TestReplyWithoutTargetsGoesToTheThread(t *testing.T) {
	s := newTestServer(t)
	th := newThread(t, s)

	// The reviewer can't see the critic's r3 under addressed visibility, so the critic
	// isn't among its recipients.
	r := replyDefault(s, th.reviewer, th.board, th.r2.Id, "Done.")
	mustStatus(t, r, nil, 201)
	if got := targets(*r.JSON201); !slices.Equal(got, []string{"@writer"}) {
		t.Fatalf("the reviewer's reply went to %v, want [@writer]", got)
	}
	if r.JSON201.ReplyToFrom == nil || *r.JSON201.ReplyToFrom != "writer" {
		t.Fatalf("reply_to_from = %v, want writer", r.JSON201.ReplyToFrom)
	}

	// A person sees the whole thread: the author of r1 first, then everyone else in it.
	r = replyDefault(s, s.owner, th.board, th.r1.Id, "Thanks, all.")
	mustStatus(t, r, nil, 201)
	if got := targets(*r.JSON201); !slices.Equal(got, []string{"@reviewer", "@writer", "@critic"}) {
		t.Fatalf("the person's reply went to %v, want [@reviewer @writer @critic]", got)
	}

	all := []string{"all"}
	r = say(s, s.owner, th.board, all, "For everyone.")
	mustStatus(t, r, nil, 201)
	explicit := reply(s, s.owner, th.board, th.root.Id, all, "Everyone should know.")
	if got := targets(explicit); !slices.Equal(got, all) {
		t.Fatalf("an explicit --to all became %v", got)
	}

	// The writer's own message to everyone has no one else in its thread.
	r = replyDefault(s, th.writer, th.board, th.other.Id, "And one more thing.")
	if code := errorCode(t, r, nil, 422); code != "reply_has_no_recipients" {
		t.Fatalf("error code %q, want reply_has_no_recipients", code)
	}
}

// A message that replies to nothing has no reply_to_from.
func TestMessageThatRepliesToNothingHasNoReplyToFrom(t *testing.T) {
	s := newTestServer(t)
	th := newThread(t, s)
	if th.root.ReplyToFrom != nil {
		t.Fatalf("reply_to_from = %q on a message that replies to nothing", *th.root.ReplyToFrom)
	}
}
