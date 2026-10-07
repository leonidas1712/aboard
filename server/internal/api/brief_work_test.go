package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestBriefKeeperFollowsTheLatestImmutableAuthor(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, writer, reviewer := s.pair("starter")
	c := s.client(writer)
	f := briefUpload(t, c, name, briefParams("brief.md"), "agent draft", 201).JSON201
	in, err := c.GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, in, err, 200)
	if in.JSON200.Work == nil || in.JSON200.Work.Brief == nil || in.JSON200.Work.Brief.Version != 1 {
		t.Fatal("latest agent author must receive the brief's keeper facts")
	}
	other, err := s.client(reviewer).GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, other, err, 200)
	if other.JSON200.Work == nil || other.JSON200.Work.Brief != nil {
		t.Fatal("another seat must not inherit keeper advice")
	}
	p := api.PutFileParams{Name: "brief.md", Brief: ptr(true), FileId: &f.Id, Base: ptr(1)}
	briefUpload(t, s.client(s.owner), name, p, "person's revision", 201)
	in, err = c.GetInboxWithResponse(context.Background(), nil)
	mustStatus(t, in, err, 200)
	if in.JSON200.Work == nil || in.JSON200.Work.Brief != nil {
		t.Fatal("a person becoming latest author must end the old agent's keeper advice")
	}
}
