package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestTaskTagsPreserveSelectionThreadAndExplicitIntent(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	owner := s.client(s.owner)
	one, err := owner.CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "First task"})
	mustStatus(t, one, err, 201)
	two, err := owner.CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Second task"})
	mustStatus(t, two, err, 201)
	started, err := s.client(writer).StartTaskWithResponse(ctx, name, one.JSON201.Ref, nil)
	mustStatus(t, started, err, 200)
	posted, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Working on " + two.JSON201.Ref})
	mustStatus(t, posted, err, 201)
	if posted.JSON201.About == nil {
		t.Fatal("task feature omitted message tags")
	}
	if tags := *posted.JSON201.About; len(tags) != 2 || tags[0].Id != one.JSON201.Id || tags[0].How != "current" || tags[1].Id != two.JSON201.Id || tags[1].How != "named" {
		t.Fatalf("current and named task tags: %+v", tags)
	}
	stands, base := "The first message is posted", 0
	noted, err := s.client(writer).UpdateTaskWithResponse(ctx, name, one.JSON201.Ref, nil, api.UpdateTaskRequest{Stands: &stands, StandsBase: &base})
	mustStatus(t, noted, err, 200)
	reply, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "I saw both tasks", ReplyTo: &posted.JSON201.Id})
	mustStatus(t, reply, err, 201)
	if reply.JSON201.About == nil {
		t.Fatal("task feature omitted reply tags")
	}
	if tags := *reply.JSON201.About; len(tags) != 2 || tags[0].How != "thread" || tags[1].How != "thread" {
		t.Fatalf("reply lost thread task tags: %+v", tags)
	}
	none := []string{}
	untagged, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "A separate thought", About: &none})
	mustStatus(t, untagged, err, 201)
	if untagged.JSON201.About == nil || len(*untagged.JSON201.About) != 0 {
		t.Fatalf("explicit empty tags inherited work: %+v", untagged.JSON201.About)
	}
	limit := 1
	page, err := s.client(writer).ListMessagesWithResponse(ctx, name, &api.ListMessagesParams{Task: &one.JSON201.Ref, Limit: &limit})
	mustStatus(t, page, err, 200)
	if len(page.JSON200.Messages) != 1 || page.JSON200.Messages[0].Id != posted.JSON201.Id || page.JSON200.NextAfter == nil {
		t.Fatalf("first task-filtered page: %+v", page.JSON200)
	}
	next, err := s.client(writer).ListMessagesWithResponse(ctx, name, &api.ListMessagesParams{Task: &one.JSON201.Ref, Limit: &limit, After: page.JSON200.NextAfter})
	mustStatus(t, next, err, 200)
	if len(next.JSON200.Messages) != 1 || next.JSON200.Messages[0].Id != reply.JSON201.Id {
		t.Fatalf("second task-filtered page: %+v", next.JSON200)
	}
	detail, err := owner.GetTaskWithResponse(ctx, name, one.JSON201.Ref)
	mustStatus(t, detail, err, 200)
	if detail.JSON200.MessageCount != 2 || detail.JSON200.ThreadCount != 1 {
		t.Fatalf("task conversation counts: %+v", detail.JSON200)
	}
	if detail.JSON200.Stands == nil || detail.JSON200.Stands.MessagesSince == nil || *detail.JSON200.Stands.MessagesSince != 1 {
		t.Fatalf("freshness must follow event order even with identical timestamps: %+v", detail.JSON200.Stands)
	}
}
