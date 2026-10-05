package api_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

// streamStartGate holds the read after authentication, before Start's transaction.
type streamStartGate struct {
	board.Store
	mu               sync.Mutex
	skip             int
	waiting, release chan struct{}
	fail             error
}

func (g *streamStartGate) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	g.mu.Lock()
	waiting, release, fail := g.waiting, g.release, g.fail
	if waiting != nil && g.skip > 0 {
		g.skip--
		waiting = nil
	} else if waiting != nil {
		g.waiting = nil
	}
	g.mu.Unlock()
	if waiting != nil {
		close(waiting)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if fail != nil {
			return fail
		}
	}
	return g.Store.Read(ctx, fn)
}

func TestStreamStartFailureDoesNotOpenAnEmptyStream(t *testing.T) {
	for _, ending := range []string{"revoked", "expired", "store failure"} {
		t.Run(ending, func(t *testing.T) {
			var gate *streamStartGate
			s := newTestServer(t, func(o *api.Options) {
				store, ok := o.Responses.(board.Store)
				if !ok {
					t.Fatal("response store does not implement board.Store")
				}
				gate = &streamStartGate{Store: store}
				o.Service = board.New(gate, notify.NewInProcess(), o.Clock, ids.New(rand.Reader), []byte("test digest key"), board.Config{ServerID: "srv_01M3W33B00TESTSERVER000000", Mode: "local", JoinHost: "localhost"}, o.Log)
			})
			created := s.createKey(s.owner, "short-lived", 3600)
			mustStatus(t, created, nil, 201)
			ctx, cancel := context.WithTimeout(context.Background(), streamWait)
			defer cancel()
			gate.mu.Lock()
			gate.skip = 1 // Authenticate reads first; Start reads next.
			waiting, release := make(chan struct{}), make(chan struct{})
			gate.waiting, gate.release = waiting, release
			if ending == "store failure" {
				gate.fail = errors.New("read failed")
			}
			gate.mu.Unlock()
			result := make(chan *rawResponse, 1)
			go func() {
				resp, err := s.streamRequest(ctx, created.JSON201.Token)
				if err != nil {
					t.Error(err)
					result <- nil
					return
				}
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Error(err)
				}
				result <- &rawResponse{Status: resp.StatusCode, Body: body}
			}()
			select {
			case <-waiting:
			case <-ctx.Done():
				t.Fatal("Start did not wait")
			}
			switch ending {
			case "revoked":
				mustStatus(t, s.revokeKey(s.owner, created.JSON201.Id), nil, 200)
			case "expired":
				s.clock.Advance(time.Hour)
			}
			close(release)
			response := <-result
			if response == nil {
				t.Fatal("no response")
			}
			status, code := 401, "unauthorized"
			if ending == "store failure" {
				status, code = 500, "internal"
			}
			if got := errorCode(t, response, nil, status); got != code {
				t.Fatalf("code %s, want %s", got, code)
			}
		})
	}
}
