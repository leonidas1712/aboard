package api_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// reply posts body as token in reply to the message with id to, addressed to targets.
func reply(s *testServer, token, boardName, to string, targets []string, body string) api.Message {
	s.t.Helper()
	ts := make([]api.Target, len(targets))
	copy(ts, targets)
	r, err := s.client(token).PostMessageWithResponse(context.Background(), boardName, nil,
		api.PostMessageRequest{Body: body, To: &ts, ReplyTo: &to})
	mustStatus(s.t, r, err, 201)
	return *r.JSON201
}

func seqsOf(ms []api.Message) []int {
	out := []int{}
	for _, m := range ms {
		out = append(out, m.Seq)
	}
	return out
}

// thread is a conversation under addressed visibility:
//
//	root   writer   -> @reviewer  asks for a review
//	r1     reviewer -> @writer    replies to root
//	r2     writer   -> @reviewer  replies to r1, a reply to a reply
//	r3     critic   -> @writer    replies to root; the reviewer may not see it
//	other  writer   -> all        replies to nothing
type thread struct {
	board                    string
	writer, reviewer, critic string
	root, r1, r2, r3, other  api.Message
}

func newThread(t *testing.T, s *testServer) thread {
	t.Helper()
	var th thread
	th.board, th.writer, th.reviewer = s.pair("recommended")
	name, role := "critic", "reviewer"
	j, err := s.client(s.owner).JoinWithResponse(context.Background(), nil, api.JoinRequest{Board: &th.board, Role: &role, Name: &name})
	mustStatus(t, j, err, 201)
	th.critic = j.JSON201.Token

	r := say(s, th.writer, th.board, []string{"@reviewer"}, "Can you review the draft?")
	mustStatus(t, r, nil, 201)
	th.root = *r.JSON201
	s.clock.Advance(time.Minute)
	th.r1 = reply(s, th.reviewer, th.board, th.root.Id, []string{"@writer"}, "Yes, after lunch.")
	s.clock.Advance(time.Minute)
	th.r2 = reply(s, th.writer, th.board, th.r1.Id, []string{"@reviewer"}, "Thanks.")
	s.clock.Advance(time.Minute)
	th.r3 = reply(s, th.critic, th.board, th.root.Id, []string{"@writer"}, "I'll look too.")
	o := say(s, th.writer, th.board, nil, "Unrelated.")
	mustStatus(t, o, nil, 201)
	th.other = *o.JSON201
	return th
}

// A reply to a reply joins the thread of the reply it answers, so a thread is one level
// deep, and its replies read oldest first from any message in it.
func TestRepliesReadTheWholeThreadFromAnyMessageInIt(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	th := newThread(t, s)
	human := s.client(s.owner)

	if th.r2.ThreadRoot == nil || *th.r2.ThreadRoot != th.root.Id || th.r2.ThreadRootSeq == nil || *th.r2.ThreadRootSeq != th.root.Seq {
		t.Fatalf("a reply to a reply: thread_root %v (#%v), want %s (#%d)", th.r2.ThreadRoot, th.r2.ThreadRootSeq, th.root.Id, th.root.Seq)
	}
	if th.root.ThreadRoot != nil || th.other.ThreadRoot != nil {
		t.Fatalf("a message that replies to nothing has a thread_root: %v, %v", th.root.ThreadRoot, th.other.ThreadRoot)
	}

	want := []int{th.r1.Seq, th.r2.Seq, th.r3.Seq}
	for _, from := range []api.Message{th.root, th.r1, th.r2, th.r3} {
		r, err := human.ListRepliesWithResponse(ctx, from.Id, nil)
		mustStatus(t, r, err, 200)
		got := r.JSON200
		if got.MessageId != from.Id || got.Root == nil || got.Root.Id != th.root.Id || !slices.Equal(seqsOf(got.Replies), want) {
			t.Fatalf("thread of #%d: %s", from.Seq, r.Body)
		}
		if got.Root.ReplyCount != 3 || got.Root.LastReplyAt == nil || !got.Root.LastReplyAt.Equal(th.r3.At) || got.NextAfter != nil {
			t.Fatalf("thread of #%d: root counts %d replies, last at %v; want 3, %v: %s", from.Seq, got.Root.ReplyCount, got.Root.LastReplyAt, th.r3.At, r.Body)
		}
	}

	// The timeline counts replies on the messages that start a thread, and only there.
	l, err := human.ListMessagesWithResponse(ctx, th.board, nil)
	mustStatus(t, l, err, 200)
	for _, m := range l.JSON200.Messages {
		wantCount := 0
		if m.Id == th.root.Id {
			wantCount = 3
		}
		if m.ReplyCount != wantCount || (wantCount == 0) != (m.LastReplyAt == nil) {
			t.Fatalf("#%d in the timeline: reply_count %d, last_reply_at %v; want %d", m.Seq, m.ReplyCount, m.LastReplyAt, wantCount)
		}
	}

	// Pages of a thread follow on with next_after.
	two := 2
	p, err := human.ListRepliesWithResponse(ctx, th.root.Id, &api.ListRepliesParams{Limit: &two})
	mustStatus(t, p, err, 200)
	if !slices.Equal(seqsOf(p.JSON200.Replies), want[:2]) || p.JSON200.NextAfter == nil || *p.JSON200.NextAfter != th.r2.Seq {
		t.Fatalf("first page of two: %s", p.Body)
	}
	p, err = human.ListRepliesWithResponse(ctx, th.root.Id, &api.ListRepliesParams{After: p.JSON200.NextAfter, Limit: &two})
	mustStatus(t, p, err, 200)
	if !slices.Equal(seqsOf(p.JSON200.Replies), want[2:]) || p.JSON200.NextAfter != nil {
		t.Fatalf("second page: %s", p.Body)
	}

	// A message without replies is a thread of its own, with no replies yet.
	o, err := human.ListRepliesWithResponse(ctx, th.other.Id, nil)
	mustStatus(t, o, err, 200)
	if o.JSON200.Root == nil || o.JSON200.Root.Id != th.other.Id || len(o.JSON200.Replies) != 0 || o.JSON200.Root.ReplyCount != 0 {
		t.Fatalf("thread of a message without replies: %s", o.Body)
	}
}

// Under addressed visibility a thread shows a reader only the replies it may see, and
// counts only those; a reader that may not see the first message gets a thread without
// it, and one that may not see the message asked for gets message_not_found.
func TestThreadsShowOnlyWhatTheReaderMaySee(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	th := newThread(t, s)

	r, err := s.client(th.reviewer).ListRepliesWithResponse(ctx, th.root.Id, nil)
	mustStatus(t, r, err, 200)
	if !slices.Equal(seqsOf(r.JSON200.Replies), []int{th.r1.Seq, th.r2.Seq}) || r.JSON200.Root == nil || r.JSON200.Root.ReplyCount != 2 ||
		!r.JSON200.Root.LastReplyAt.Equal(th.r2.At) {
		t.Fatalf("the reviewer's view of the thread: %s", r.Body)
	}
	l, err := s.client(th.reviewer).ListMessagesWithResponse(ctx, th.board, nil)
	mustStatus(t, l, err, 200)
	if got := l.JSON200.Messages[0]; got.Id != th.root.Id || got.ReplyCount != 2 {
		t.Fatalf("the reviewer's timeline counts %d replies on #%d, want 2", got.ReplyCount, got.Seq)
	}

	// The critic sees its own reply, but neither the first message nor the others.
	c, err := s.client(th.critic).ListRepliesWithResponse(ctx, th.r3.Id, nil)
	mustStatus(t, c, err, 200)
	if c.JSON200.Root != nil || !slices.Equal(seqsOf(c.JSON200.Replies), []int{th.r3.Seq}) {
		t.Fatalf("the critic's view of the thread: %s", c.Body)
	}
	for _, id := range []string{th.root.Id, th.r1.Id, "msg_00000000000000000000000000"} {
		nf, err := s.client(th.critic).ListRepliesWithResponse(ctx, id, nil)
		if code := errorCode(t, nf, err, 404); code != "message_not_found" {
			t.Fatalf("the critic reading the thread of %s: %s", id, code)
		}
	}

	// A person on another board can't read the thread either.
	other := s.addHuman("blair")
	nf, err := s.client(other).ListRepliesWithResponse(ctx, th.root.Id, nil)
	if code := errorCode(t, nf, err, 404); code != "message_not_found" {
		t.Fatalf("someone not on the board: %s", code)
	}
}

// With wait, reading a thread holds until a reply arrives.
func TestRepliesWaitForTheNextReply(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	th := newThread(t, s)

	after, wait := th.r3.Seq, 30
	got := make(chan *api.ListRepliesResponse, 1)
	go func() {
		r, err := s.client(s.owner).ListRepliesWithResponse(ctx, th.root.Id, &api.ListRepliesParams{After: &after, Wait: &wait})
		if err != nil {
			t.Error(err)
		}
		got <- r
	}()
	// The reply may be posted before or after the request starts waiting; either way it
	// is returned.
	r4 := reply(s, th.reviewer, th.board, th.r3.Id, []string{"@critic"}, "Welcome.")
	select {
	case r := <-got:
		if r.StatusCode() != 200 || !slices.Equal(seqsOf(r.JSON200.Replies), []int{r4.Seq}) {
			t.Fatalf("waiting for a reply: %s", r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait didn't end when a reply was posted")
	}
}
