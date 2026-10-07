package api_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestAskBlocksCurrentTaskAndAuthorizedReplyAnswers(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	task, err := s.client(writer).CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Choose storage", Start: ptrTrue()})
	mustStatus(t, task, err, 201)
	ask, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Which storage?", Ask: &api.AskRequest{Options: &[]string{"SQLite", "Other"}}})
	mustStatus(t, ask, err, 201)
	if ask.JSON201.Ask == nil || !ask.JSON201.Ask.Blocking || ask.JSON201.Ask.To.Name != "alex" || ask.JSON201.Ask.Task == nil {
		t.Fatalf("ask missing projection: %+v", ask.JSON201)
	}
	got, err := s.client(reviewer).GetTaskWithResponse(ctx, name, task.JSON201.Ref)
	mustStatus(t, got, err, 200)
	if !got.JSON200.Blocked || got.JSON200.BlockedCount != 1 {
		t.Fatalf("task not blocked: %+v", got.JSON200)
	}
	stranger, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Maybe", ReplyTo: &ask.JSON201.Id})
	mustStatus(t, stranger, err, 201)
	if stranger.JSON201.Answer != nil {
		t.Fatal("ordinary reply became answer")
	}
	option := 1
	answered, err := s.client(s.owner).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "SQLite", ReplyTo: &ask.JSON201.Id, Answer: &api.AnswerRequest{Option: &option}})
	mustStatus(t, answered, err, 201)
	if answered.JSON201.Answer == nil || answered.JSON201.Answer.AskId != ask.JSON201.Id {
		t.Fatal("answer not recorded")
	}
	list, err := s.client(writer).ListAsksWithResponse(ctx, &api.ListAsksParams{State: func() *api.ListAsksParamsState { v := api.ListAsksParamsStateAll; return &v }()})
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Asks) != 1 || list.JSON200.Asks[0].Ask.State != api.AskStateAnswered {
		t.Fatalf("ask list: %+v", list.JSON200)
	}
	got, err = s.client(writer).GetTaskWithResponse(ctx, name, task.JSON201.Ref)
	mustStatus(t, got, err, 200)
	if got.JSON200.Blocked {
		t.Fatal("answer did not unblock task")
	}
}

func TestAskTimeWithdrawalAndOverrideRemainRecorded(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	future := s.clock.Now().Add(time.Hour)
	defaultChoice := "SQLite"
	posted, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Going ahead?", To: &[]string{"@reviewer"}, Ask: &api.AskRequest{Options: &[]string{"SQLite", "Other"}, GoingWith: &defaultChoice, GoingAt: &future}})
	mustStatus(t, posted, err, 201)
	if posted.JSON201.Ask.Blocking || posted.JSON201.Ask.State != api.AskStateOpen {
		t.Fatal("future default is not an open nonblocking ask")
	}
	s.clock.Advance(time.Hour)
	state := api.ListAsksParamsStateAll
	read, err := s.client(writer).ListAsksWithResponse(ctx, &api.ListAsksParams{State: &state})
	mustStatus(t, read, err, 200)
	if read.JSON200.Asks[0].Ask.State != api.AskStateWentWith {
		t.Fatal("time boundary did not derive went_with")
	}
	option := 2
	answered, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Use other", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{Option: &option}})
	mustStatus(t, answered, err, 201)
	option = 1
	override, err := s.client(s.owner).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "SQLite after all", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{Option: &option}})
	mustStatus(t, override, err, 201)
	read, err = s.client(writer).ListAsksWithResponse(ctx, &api.ListAsksParams{State: &state})
	mustStatus(t, read, err, 200)
	if read.JSON200.Asks[0].Ask.AnswerSeq == nil || *read.JSON200.Asks[0].Ask.AnswerSeq != override.JSON201.Seq {
		t.Fatal("latest answer did not replace projected answer")
	}
	withdrawn, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "No longer needed", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{Withdrawn: ptrTrue()}})
	mustStatus(t, withdrawn, err, 201)
	late, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Another answer", ReplyTo: &posted.JSON201.Id})
	mustStatus(t, late, err, 409)
	events, err := s.client(s.owner).ListEventsWithResponse(ctx, name, nil)
	mustStatus(t, events, err, 200)
	seen := 0
	for _, e := range events.JSON200.Events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if v.Type == "message.posted" {
			seen++
		}
	}
	if seen != 4 {
		t.Fatalf("ask/answers not all retained: %d", seen)
	}
}

func TestHiddenBlockingAskCountsWithoutDisclosingRecipient(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("recommended")
	task, err := s.client(writer).CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Hidden decision", Start: ptrTrue()})
	mustStatus(t, task, err, 201)
	posted, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Private decision?", Ask: &api.AskRequest{}})
	mustStatus(t, posted, err, 201)
	got, err := s.client(reviewer).GetTaskWithResponse(ctx, name, task.JSON201.Id)
	mustStatus(t, got, err, 200)
	if !got.JSON200.Blocked || got.JSON200.BlockedCount != 1 || len(got.JSON200.BlockedOn) != 0 {
		t.Fatalf("hidden ask recipient leaked: %+v", got.JSON200)
	}
	list, err := s.client(reviewer).ListAsksWithResponse(ctx, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Asks) != 0 {
		t.Fatal("hidden ask leaked in list")
	}
	view, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, view, err, 200)
	if view.JSON200.AsksToMe == nil || view.JSON200.AsksToMe.Blocking != 1 || view.JSON200.NeedsReply == nil || *view.JSON200.NeedsReply != 1 {
		t.Fatal("board attention omitted ask")
	}
	bad, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "Cannot answer", To: &[]string{"@writer"}, ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{}})
	mustStatus(t, bad, err, 404)
}

func TestAskAnswerAuthorityAndUnsupportedApprovalFailClosed(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	tb := s.teamBoard()
	posted, err := s.client(tb.mayaAgent.token).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Sam choose", To: &[]string{"@sam"}, Ask: &api.AskRequest{Options: &[]string{"one"}}})
	mustStatus(t, posted, err, 201)
	bad, err := s.client(s.owner).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Admin isn't asked", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{}})
	mustStatus(t, bad, err, 403)
	ordinary, err := s.client(s.owner).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Only a comment", ReplyTo: &posted.JSON201.Id})
	mustStatus(t, ordinary, err, 201)
	if ordinary.JSON201.Answer != nil {
		t.Fatal("admin answered another person's ask")
	}
	option := 2
	bad, err = s.client(tb.sam).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "missing option", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{Option: &option}})
	mustStatus(t, bad, err, 422)
	bad, err = s.client(tb.maya).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Only asker withdraws", ReplyTo: &posted.JSON201.Id, Answer: &api.AnswerRequest{Withdrawn: ptrTrue()}})
	mustStatus(t, bad, err, 403)
	answered, err := s.client(tb.maya).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Owner can steer", ReplyTo: &posted.JSON201.Id})
	mustStatus(t, answered, err, 201)
	if answered.JSON201.Answer == nil {
		t.Fatal("asker's person couldn't answer")
	}
	// Unsupported approval must not create an ordinary ask pretending to approve files.
	bad, err = s.client(tb.mayaAgent.token).PostMessageWithResponse(ctx, tb.board, nil, api.PostMessageRequest{Body: "Approve", To: &[]string{"@maya"}, Ask: &api.AskRequest{Approval: ptrTrue(), Options: &[]string{"Approve"}}})
	mustStatus(t, bad, err, 422)
}

func TestAskListRechecksMembershipAndCachedWriteCannotLeak(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tb := s.teamBoard()
	ctx := context.Background()
	key := "ask-before-leave"
	input := api.PostMessageRequest{Body: "Decide on this", To: &[]string{"@sam"}, Ask: &api.AskRequest{}}
	posted, err := s.client(tb.maya).PostMessageWithResponse(ctx, tb.board, &api.PostMessageParams{IdempotencyKey: &key}, input)
	mustStatus(t, posted, err, 201)
	left, err := s.client(tb.maya).LeaveBoardWithResponse(ctx, tb.board, nil)
	mustStatus(t, left, err, 200)
	list, err := s.client(tb.maya).ListAsksWithResponse(ctx, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Asks) != 0 {
		t.Fatal("asks remained after leaving board")
	}
	list, err = s.client(tb.maya).ListAsksWithResponse(ctx, &api.ListAsksParams{Board: &tb.board})
	mustStatus(t, list, err, 403)
	replay, err := s.client(tb.maya).PostMessageWithResponse(ctx, tb.board, &api.PostMessageParams{IdempotencyKey: &key}, input)
	if err != nil {
		t.Fatal(err)
	}
	if replay.StatusCode() == 201 {
		t.Fatal("cached message write leaked ask to a person off board")
	}
}

func TestAskInboxAndIndependentBlockingAsks(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	task, err := s.client(writer).CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Two decisions", Start: ptrTrue()})
	mustStatus(t, task, err, 201)
	ids := []string{}
	for _, body := range []string{"First decision", "Second decision"} {
		m, err := s.client(writer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: body, To: &[]string{"@reviewer"}, Ask: &api.AskRequest{}})
		mustStatus(t, m, err, 201)
		ids = append(ids, m.JSON201.Id)
	}
	box, err := s.client(reviewer).GetInboxWithResponse(ctx, nil)
	mustStatus(t, box, err, 200)
	if box.JSON200.Work == nil || box.JSON200.Work.AsksToIt == nil || *box.JSON200.Work.AsksToIt != 2 || len(box.JSON200.Messages) != 2 {
		t.Fatalf("inbox omitted asks: %+v", box.JSON200)
	}
	answered, err := s.client(reviewer).PostMessageWithResponse(ctx, name, nil, api.PostMessageRequest{Body: "First answered", ReplyTo: &ids[0]})
	mustStatus(t, answered, err, 201)
	got, err := s.client(writer).GetTaskWithResponse(ctx, name, task.JSON201.Id)
	mustStatus(t, got, err, 200)
	if !got.JSON200.Blocked || got.JSON200.BlockedCount != 1 {
		t.Fatal("one answer incorrectly unblocked two asks")
	}
	box, err = s.client(writer).GetInboxWithResponse(ctx, nil)
	mustStatus(t, box, err, 200)
	if box.JSON200.Work == nil || box.JSON200.Work.AsksWaiting != 1 {
		t.Fatalf("own asks waiting not counted: %+v", box.JSON200.Work)
	}
}
