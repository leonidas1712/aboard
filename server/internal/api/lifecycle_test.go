package api_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

func (s *testServer) lifecycleCall(token, method, path, key string, body any) call {
	s.t.Helper()
	return s.send(method, path, body, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
	})
}

func requireLifecycleCall(t *testing.T, got call, status int, code string) {
	t.Helper()
	if got.status != status || (code != "" && got.code() != code) {
		t.Fatalf("response status=%d code=%s, want status=%d code=%s", got.status, got.code(), status, code)
	}
}

func TestDeletingABoardRefusesItsCachedCreationAndContent(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	const name = "cached-lifecycle"
	create := map[string]any{"name": name}
	created := s.lifecycleCall(s.owner, "POST", "/v1/boards", "creation", create)
	requireLifecycleCall(t, created, 201, "")
	postPath := "/v1/boards/" + name + "/messages"
	post := map[string]any{"body": "retained historical content"}
	posted := s.lifecycleCall(s.owner, "POST", postPath, "message", post)
	requireLifecycleCall(t, posted, 201, "")
	lifecycle := "/v1/boards/" + name
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", lifecycle+"/archive", "archive", nil), 200, "")
	// An archive keeps a committed content replay readable without publishing again.
	if replay := s.lifecycleCall(s.owner, "POST", postPath, "message", post); replay.raw != posted.raw {
		t.Fatal("archive changed a committed content replay")
	}
	deleted := s.lifecycleCall(s.owner, "POST", lifecycle+"/delete", "delete", nil)
	requireLifecycleCall(t, deleted, 200, "")
	if len(deleted.body) != 3 || deleted.body["lifecycle"] != "deleted" {
		t.Fatal("deletion receipt contains more than id, lifecycle and changed")
	}
	for _, replay := range []call{
		s.lifecycleCall(s.owner, "POST", "/v1/boards", "creation", create),
		s.lifecycleCall(s.owner, "POST", postPath, "message", post),
		s.lifecycleCall(s.owner, "POST", lifecycle+"/archive", "archive", nil),
	} {
		requireLifecycleCall(t, replay, 404, "board_not_found")
		if strings.Contains(replay.raw, "retained historical content") {
			t.Fatal("cached response exposed deleted content")
		}
	}
	// Only the original successful deletion receipt can be retried.
	if replay := s.lifecycleCall(s.owner, "POST", lifecycle+"/delete", "delete", nil); replay.raw != deleted.raw {
		t.Fatal("successful deletion retry lost its minimal committed receipt")
	}
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", lifecycle+"/delete", "new-delete", nil), 404, "board_not_found")
}

func TestOutsideAdminLifecycleUsesOnlyAHiddenBoardID(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name := s.privateBoard(maya)
	listed, err := s.client(s.owner).ListBoardsWithResponse(context.Background(), &api.ListBoardsParams{All: ptr(true)})
	mustStatus(t, listed, err, 200)
	id := (*listed.JSON200.HiddenBoards)[0].Id
	for _, operation := range []string{"archive", "restore", "archive", "delete"} {
		got := s.lifecycleCall(s.owner, "POST", "/v1/boards/"+id+"/"+operation, "", nil)
		requireLifecycleCall(t, got, 200, "")
		if len(got.body) != 3 || got.body["id"] != id || strings.Contains(got.raw, name) || strings.Contains(got.raw, "secret plans") {
			t.Fatalf("%s returned private-board metadata beyond its minimal receipt", operation)
		}
		// Housekeeping never grants the admin membership or content access.
		requireLifecycleCall(t, s.lifecycleCall(s.owner, "GET", "/v1/boards/"+name, "", nil), 404, "board_not_found")
	}
}

func TestArchivedContentRefusalsDoNotBecomeCachedWritesAfterRestore(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	base := "/v1/boards/" + name
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", base+"/archive", "", nil), 200, "")
	body := map[string]any{"body": "not yet committed"}
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", base+"/messages", "refused", body), 409, "board_archived")
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", base+"/restore", "", nil), 200, "")
	// Idempotency preserves a refusal; a new operation needs a new key.
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", base+"/messages", "refused", body), 409, "board_archived")
	requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", base+"/messages", "new-write", body), 201, "")
}

func TestCachedContentChecksAuthorityAfterWaitingForItsRead(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"revoked", "expired", "deleted"} {
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
			key := s.createKey(s.owner, "cached-content", 3600)
			mustStatus(t, key, nil, 201)
			const name = "replay-read-race"
			requireLifecycleCall(t, s.lifecycleCall(key.JSON201.Token, "POST", "/v1/boards", "", map[string]any{"name": name}), 201, "")
			path := "/v1/boards/" + name + "/messages"
			body := map[string]any{"body": "not available after access ends"}
			requireLifecycleCall(t, s.lifecycleCall(key.JSON201.Token, "POST", path, "saved", body), 201, "")
			waiting, release := make(chan struct{}), make(chan struct{})
			gate.mu.Lock()
			gate.skip, gate.waiting, gate.release = 1, waiting, release
			gate.mu.Unlock()
			result := make(chan call, 1)
			go func() { result <- s.lifecycleCall(key.JSON201.Token, "POST", path, "saved", body) }()
			ctx, cancel := context.WithTimeout(context.Background(), streamWait)
			defer cancel()
			select {
			case <-waiting:
			case <-result:
				t.Fatal("cached content returned without a fresh transactional read")
			case <-ctx.Done():
				t.Fatal("cached-content read did not reach its entry barrier")
			}
			switch ending {
			case "revoked":
				mustStatus(t, s.revokeKey(s.owner, key.JSON201.Id), nil, 200)
			case "expired":
				s.clock.Advance(time.Hour)
			case "deleted":
				requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", "/v1/boards/"+name+"/archive", "", nil), 200, "")
				requireLifecycleCall(t, s.lifecycleCall(s.owner, "POST", "/v1/boards/"+name+"/delete", "", nil), 200, "")
			}
			close(release)
			select {
			case got := <-result:
				if ending == "deleted" {
					requireLifecycleCall(t, got, 404, "board_not_found")
				} else {
					requireLifecycleCall(t, got, 401, "unauthorized")
				}
				if strings.Contains(got.raw, "not available after access ends") {
					t.Fatal("cached content escaped the current-authority check")
				}
			case <-ctx.Done():
				t.Fatal("cached-content read did not finish after its barrier")
			}
		})
	}
}
