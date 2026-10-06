package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestIdempotencyKeyStartsANewRequestAfter24Hours(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"first", "different"} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name, writer, _ := s.pair("starter")
			c := s.client(writer)
			key := "expires"
			s.clock.Advance(17 * time.Nanosecond)
			post := func(body string) *api.PostMessageResponse {
				r, err := c.PostMessageWithResponse(context.Background(), name,
					&api.PostMessageParams{IdempotencyKey: &key}, api.PostMessageRequest{Body: body})
				mustStatus(t, r, err, 201)
				return r
			}
			first := post("first")
			s.clock.Advance(24*time.Hour - time.Nanosecond)
			before := post("first")
			if before.JSON201.Id != first.JSON201.Id || before.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
				t.Fatal("the answer expired before 24 hours")
			}
			s.clock.Advance(time.Nanosecond)
			fresh := post(body)
			if fresh.JSON201.Id == first.JSON201.Id || fresh.HTTPResponse.Header.Get("Idempotent-Replayed") != "" {
				t.Fatal("an expired answer was replayed instead of running a new request")
			}
			again := post(body)
			if again.JSON201.Id != fresh.JSON201.Id || again.HTTPResponse.Header.Get("Idempotent-Replayed") != "true" {
				t.Fatal("the new request's answer was not retained for its own retries")
			}
			page, err := c.ListMessagesWithResponse(context.Background(), name, nil)
			mustStatus(t, page, err, 200)
			if len(page.JSON200.Messages) != 2 {
				t.Fatalf("got %d messages, want exactly two committed writes", len(page.JSON200.Messages))
			}
		})
	}
}
