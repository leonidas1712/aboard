package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func (s *testServer) needsReply(token, boardName string) *int {
	s.t.Helper()
	b, err := s.client(token).GetBoardWithResponse(context.Background(), boardName)
	mustStatus(s.t, b, err, 200)
	var got struct {
		NeedsReply *int `json:"needs_reply"`
	}
	if err := json.Unmarshal(b.Body, &got); err != nil {
		s.t.Fatal(err)
	}
	l, err := s.client(token).ListBoardsWithResponse(context.Background(), nil)
	mustStatus(s.t, l, err, 200)
	var list struct {
		Boards []struct {
			Name       string `json:"name"`
			NeedsReply *int   `json:"needs_reply"`
		} `json:"boards"`
	}
	if err := json.Unmarshal(l.Body, &list); err != nil {
		s.t.Fatal(err)
	}
	for _, row := range list.Boards {
		if row.Name == boardName && ((row.NeedsReply == nil) != (got.NeedsReply == nil) || row.NeedsReply != nil && *row.NeedsReply != *got.NeedsReply) {
			s.t.Fatalf("board and list disagree: %s / %s", b.Body, l.Body)
		}
	}
	return got.NeedsReply
}

func TestQuestionsNeedThePersonsOwnDirectReply(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	b, writer, reviewer := s.pair("starter")
	check := func(want int) {
		t.Helper()
		got := s.needsReply(s.owner, b)
		if got == nil || *got != want {
			t.Fatalf("needs_reply = %v, want %d", got, want)
		}
	}
	post := func(token string, to []api.Target, reply *string) *api.Message {
		t.Helper()
		r, err := s.client(token).PostMessageWithResponse(ctx, b, nil, api.PostMessageRequest{Body: "Please answer", To: &to, ReplyTo: reply, ExpectsReply: ptrTrue()})
		mustStatus(t, r, err, 201)
		return r.JSON201
	}
	check(0)
	outsider := s.addHuman("outside")
	if got := s.needsReply(outsider, b); got != nil {
		t.Fatalf("nonmember attention disclosed: %d", *got)
	}
	q := post(writer, []api.Target{"@alex", "@reviewer"}, nil)
	check(1)
	if got := s.needsReply(writer, b); got != nil {
		t.Fatalf("agent attention disclosed: %d", *got)
	}
	post(writer, []api.Target{"all"}, nil)
	post(writer, []api.Target{"@reviewer"}, nil)
	post(s.owner, []api.Target{"@alex"}, nil)
	check(1)
	mustStatus(t, s.ackBoard(s.owner, b, q.Seq), nil, 200)
	check(1)
	followup := post(writer, []api.Target{"all"}, &q.Id)
	post(s.owner, []api.Target{"all"}, &followup.Id)
	post(reviewer, []api.Target{"all"}, &q.Id)
	check(1)
	post(s.owner, []api.Target{"all"}, &q.Id)
	check(0)
}

func ptrTrue() *bool { value := true; return &value }
