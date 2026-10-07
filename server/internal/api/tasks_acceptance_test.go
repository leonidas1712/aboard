package api_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestTaskClaimHasOneOwnerAndSelectsTheirCurrentTask(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, reviewer := s.pair("starter")
	created, err := s.client(s.owner).CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Check the claim race"})
	mustStatus(t, created, err, 201)
	task := created.JSON201
	results := make(chan *api.StartTaskResponse, 2)
	failed := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, token := range []string{writer, reviewer} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, callErr := s.client(token).StartTaskWithResponse(ctx, name, task.Ref, nil)
			results <- r
			failed <- callErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failed)
	for callErr := range failed {
		if callErr != nil {
			t.Fatal(callErr)
		}
	}
	wins, taken := 0, 0
	for r := range results {
		switch r.StatusCode() {
		case 200:
			wins++
		case 409:
			if r.JSON409 == nil || r.JSON409.Error.Code != "task_taken" {
				t.Fatalf("claim refusal: %s", r.Body)
			}
			taken++
		default:
			t.Fatalf("claim status: %d %s", r.StatusCode(), r.Body)
		}
	}
	if wins != 1 || taken != 1 {
		t.Fatalf("wins=%d taken=%d", wins, taken)
	}
	selected := 0
	for _, token := range []string{writer, reviewer} {
		me, err := s.client(token).GetMeWithResponse(ctx)
		mustStatus(t, me, err, 200)
		if current := me.JSON200.CurrentTask; current != nil {
			if current.Id != task.Id || current.Ref != task.Ref {
				t.Fatalf("current task differs from the claimed task: %+v", current)
			}
			selected++
		}
	}
	if selected != 1 {
		t.Fatalf("agents with the claimed task selected: %d, want 1", selected)
	}
}

func TestTaskReplayRequiresCurrentBoardMembership(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	person := s.addHuman("sam")
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "sam"})
	mustStatus(t, added, err, 201)
	c := s.client(person)
	key := "task-before-leave"
	body := api.CreateTaskRequest{Title: "A members-only task"}
	created, err := c.CreateTaskWithResponse(ctx, name, &api.CreateTaskParams{IdempotencyKey: &key}, body)
	mustStatus(t, created, err, 201)
	left, err := c.LeaveBoardWithResponse(ctx, name, nil)
	mustStatus(t, left, err, 200)
	replayed, err := c.CreateTaskWithResponse(ctx, name, &api.CreateTaskParams{IdempotencyKey: &key}, body)
	if code := errorCode(t, replayed, err, 403); code != "not_on_board" {
		t.Fatalf("task replay after leave: %s", code)
	}
}

func TestTaskCurrentIsExplicitNullBeforeAndAfterWork(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, _ := s.pair("starter")
	c := s.client(writer)
	check := func(taskID string) {
		t.Helper()
		me, err := c.GetMeWithResponse(ctx)
		mustStatus(t, me, err, 200)
		var own map[string]json.RawMessage
		if err := json.Unmarshal(me.Body, &own); err != nil {
			t.Fatal(err)
		}
		checkCurrent := func(raw json.RawMessage) {
			t.Helper()
			if taskID == "" {
				if string(raw) != "null" {
					t.Fatalf("current_task = %s, want explicit null", raw)
				}
				return
			}
			var current api.TaskRef
			if err := json.Unmarshal(raw, &current); err != nil {
				t.Fatal(err)
			}
			if current.Id != taskID {
				t.Fatalf("current_task ID = %q, want %q", current.Id, taskID)
			}
		}
		checkCurrent(own["current_task"])
		members, err := c.ListMembersWithResponse(ctx, name, nil)
		mustStatus(t, members, err, 200)
		var page struct {
			Members []map[string]json.RawMessage `json:"members"`
		}
		if err := json.Unmarshal(members.Body, &page); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, member := range page.Members {
			var id string
			if err := json.Unmarshal(member["id"], &id); err != nil {
				t.Fatal(err)
			}
			if id == me.JSON200.Id {
				found = true
				checkCurrent(member["current_task"])
			}
		}
		if !found {
			t.Fatal("acting agent missing from members")
		}
	}
	check("")
	start := true
	started, err := c.CreateTaskWithResponse(ctx, name, nil, api.CreateTaskRequest{Title: "Check explicit current task", Start: &start})
	mustStatus(t, started, err, 201)
	check(started.JSON201.Id)
	done, err := c.FinishTaskWithResponse(ctx, name, started.JSON201.Id, nil, api.FinishTaskRequest{Note: "Checked"})
	mustStatus(t, done, err, 200)
	check("")
}
