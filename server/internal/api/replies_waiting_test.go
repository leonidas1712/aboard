package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func (s *testServer) waitingReplies(token string, params *api.ListRepliesWaitingParams) api.RepliesWaiting {
	s.t.Helper()
	r, err := s.client(token).ListRepliesWaitingWithResponse(context.Background(), params)
	mustStatus(s.t, r, err, http.StatusOK)
	return *r.JSON200
}

func TestRepliesWaitingKeepCurrentPersonAuthorityAndDoNotExposeOtherRequests(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, peer := s.pair("starter")
	maya := s.addHuman("maya")
	request := s.expectReply(writer, name, "private request", nil)
	if got := s.waitingReplies(maya, nil); got.Count != 0 || len(got.Messages) != 0 {
		t.Fatalf("nonmember saw a request: %+v", got)
	}
	r, err := s.client(maya).ListRepliesWaitingWithResponse(ctx, &api.ListRepliesWaitingParams{Board: &name})
	mustStatus(t, r, err, http.StatusForbidden)
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "maya"})
	mustStatus(t, added, err, http.StatusCreated)
	if got := s.waitingReplies(maya, nil); got.Count != 0 || len(got.Messages) != 0 {
		t.Fatalf("membership inherited another person's request: %+v", got)
	}
	// Broadcasts, unaddressed information and structured asks have their own reads.
	expect := true
	broadcast, err := s.client(s.owner).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Anyone?", ExpectsReply: &expect})
	mustStatus(t, broadcast, err, http.StatusCreated)
	info := say(s, writer, name, []string{"@alex"}, "Information")
	mustStatus(t, info, nil, http.StatusCreated)
	to := []string{"@alex"}
	ask, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Choose", To: &to, Ask: &api.AskRequest{}})
	mustStatus(t, ask, err, http.StatusCreated)
	before, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, before, err, http.StatusOK)
	got := s.waitingReplies(s.owner, nil)
	if got.Count != 1 || len(got.Messages) != 1 || got.Messages[0].Id != request.Id {
		t.Fatalf("unrelated messages entered replies waiting: %+v", got)
	}
	after, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, after, err, http.StatusOK)
	if before.JSON200.HeadSeq != after.JSON200.HeadSeq || *before.JSON200.ReadUpTo != *after.JSON200.ReadUpTo {
		t.Fatalf("Inbox read changed the record or cursor: before=%+v after=%+v", before.JSON200, after.JSON200)
	}
	for _, token := range []string{peer, s.delegate(s.owner)} {
		r, err := s.client(token).ListRepliesWaitingWithResponse(ctx, nil)
		mustStatus(t, r, err, http.StatusForbidden)
	}
	id, key := s.newKey(s.owner, "revoked-inbox")
	s.revokeKey(s.owner, id)
	r, err = s.client(key).ListRepliesWaitingWithResponse(ctx, nil)
	mustStatus(t, r, err, http.StatusUnauthorized)
	status, raw := s.raw("POST", "/v1/boards/"+name+"/archive", s.owner, nil)
	if status != http.StatusOK {
		t.Fatalf("archive: %d %s", status, raw)
	}
	if got := s.waitingReplies(s.owner, nil); got.Count != 0 || len(got.Messages) != 0 {
		t.Fatalf("archived board stayed in Inbox: %+v", got)
	}
	status, raw = s.raw("POST", "/v1/boards/"+name+"/restore", s.owner, nil)
	if status != http.StatusOK {
		t.Fatalf("restore: %d %s", status, raw)
	}
	status, raw = s.raw("POST", "/v1/boards/"+name+"/owners", s.owner, map[string]string{"handle": "maya"})
	if status != http.StatusOK {
		t.Fatalf("owner: %d %s", status, raw)
	}
	status, raw = s.raw("POST", "/v1/boards/"+name+"/leave", s.owner, nil)
	if status != http.StatusOK {
		t.Fatalf("leave: %d %s", status, raw)
	}
	status, raw = s.raw("GET", "/v1/me/replies-waiting", s.owner, nil)
	if status != http.StatusOK || strings.Contains(raw, request.Id) || !strings.Contains(raw, `"count":0`) {
		t.Fatalf("former member saw request: %d %s", status, raw)
	}
}

func (s *testServer) expectReply(token, name, text string, reply *string) api.Message {
	s.t.Helper()
	to, expect := []string{"@alex"}, true
	r, err := s.client(token).PostMessageWithResponse(context.Background(), name, nil, api.PostMessageRequest{Body: text, To: &to, ExpectsReply: &expect, ReplyTo: reply})
	mustStatus(s.t, r, err, http.StatusCreated)
	return *r.JSON201
}

func TestRepliesWaitingFollowThePersonAndNewestRequestAcrossBoards(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	first, writer, peer := s.pair("starter")
	second, other, _ := s.pair("starter")
	root := s.expectReply(writer, first, "Please review this", nil)
	s.clock.Advance(time.Second)
	newer := s.expectReply(other, second, "Please check this too", nil)
	got := s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Limit: ptr(1)})
	if got.Count != 2 || !got.More || len(got.Messages) != 1 || got.Messages[0].Id != newer.Id || got.Messages[0].Board != second {
		t.Fatalf("cross-board count and order: %+v", got)
	}
	got = s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Board: &first})
	if got.Count != 1 || got.More || len(got.Messages) != 1 || got.Messages[0].Id != root.Id {
		t.Fatalf("board filter: %+v", got)
	}
	// A peer's reply does not answer for the person, even when the peer is their agent.
	r, err := s.client(peer).PostMessageWithResponse(context.Background(), first, nil, api.PostMessageRequest{Body: "I can help", ReplyTo: &root.Id})
	mustStatus(t, r, err, http.StatusCreated)
	if got := s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Board: &first}); got.Count != 1 {
		t.Fatalf("peer cleared the person's request: %+v", got)
	}
	s.clock.Advance(time.Second)
	latest := s.expectReply(writer, first, "Updated request", &r.JSON201.Id)
	got = s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Board: &first})
	if got.Count != 1 || len(got.Messages) != 1 || got.Messages[0].Id != latest.Id {
		t.Fatalf("newest same-sender request must replace the old one: %+v", got)
	}
	// A read position does not dismiss the request.
	ack, err := s.client(s.owner).AckBoardWithResponse(context.Background(), first, nil, api.AckBoardJSONRequestBody{UpTo: latest.Seq})
	mustStatus(t, ack, err, http.StatusOK)
	if got := s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Board: &first}); got.Count != 1 {
		t.Fatalf("read acknowledgement cleared a request: %+v", got)
	}
	_, phone := s.newKey(s.owner, "phone")
	answered, err := s.client(phone).PostMessageWithResponse(context.Background(), first, nil, api.PostMessageRequest{Body: "Reviewed", ReplyTo: &r.JSON201.Id})
	mustStatus(t, answered, err, http.StatusCreated)
	if got := s.waitingReplies(s.browserToken(s.owner), &api.ListRepliesWaitingParams{Board: &first}); got.Count != 0 || len(got.Messages) != 0 || got.More {
		t.Fatalf("person's reply-to-reply from another login did not clear: %+v", got)
	}
	// An earlier answer does not dismiss a new request in the same thread.
	last := s.expectReply(writer, first, "One more question", &latest.Id)
	if got := s.waitingReplies(s.owner, &api.ListRepliesWaitingParams{Board: &first}); got.Count != 1 || got.Messages[0].Id != last.Id {
		t.Fatalf("old reply cleared a newer request: %+v", got)
	}
	for _, token := range []string{writer, ""} {
		r, err := s.client(token).ListRepliesWaitingWithResponse(context.Background(), nil)
		want := http.StatusForbidden
		if token == "" {
			want = http.StatusUnauthorized
		}
		mustStatus(t, r, err, want)
	}
}
