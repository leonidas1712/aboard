package api_test

import (
	"context"
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
}
