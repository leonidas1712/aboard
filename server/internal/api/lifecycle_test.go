package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
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
