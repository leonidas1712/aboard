package apiserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

func TestFileReferencesReachDeliveryWithoutEmbeddingBytes(t *testing.T) {
	refs := []api.FileRef{{Id: "fil_reference", Name: "notes/api.md", Version: 2, Digest: "sha256:digest"}}
	m := TextMessage(api.Message{Board: "general", Body: "Please read the attachment", Files: &refs})
	text := deliverytext.Format(m)
	if !strings.Contains(text, "notes/api.md") || !strings.Contains(text, "--version 2") || !strings.Contains(text, "--board") {
		t.Fatalf("attachment reference missing: %s", text)
	}
	if m.Body != "Please read the attachment" {
		t.Fatal("message body changed")
	}
}

func TestTaskWorkOnlyUsesTheExactSeatInbox(t *testing.T) {
	for _, id := range []string{"mem_expected", "mem_other"} {
		t.Run(id, func(t *testing.T) {
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/me/inbox" {
					writes++
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer aba_seat" {
					t.Error("wrong credential")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"board": "docs", "agent": "writer", "member_id": id, "messages": []any{}, "cursor": 0, "more": false, "work": map[string]any{"current_task": nil, "open_tasks": 2, "oldest_open": map[string]any{"id": "tsk_one", "ref": "CHK-17", "title": "review"}, "posts_without_task": 3, "asks_waiting": 0, "line": nil, "brief": nil, "nudges": false}})
			}))
			defer srv.Close()
			ref := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_expected"}
			s := New(srv.URL, tokens{agents: map[delivery.AgentRef]string{ref: "aba_seat"}}, rand.Reader)
			work, e := s.TaskWork(context.Background(), ref)
			if id == "mem_other" {
				if !errors.Is(e, delivery.ErrUnauthorized) || work != nil {
					t.Fatalf("wrong seat accepted: %+v %v", work, e)
				}
			} else if e != nil || work.OpenTasks != 2 || work.OldestOpen.Ref != "CHK-17" || work.Nudges {
				t.Fatalf("work=%+v err=%v", work, e)
			}
			if writes != 0 {
				t.Fatal("work read wrote state")
			}
		})
	}
}

func TestTaskMessageMappingPreservesRecordedReferences(t *testing.T) {
	tags := []api.TaskTag{{Id: "tsk_one", Ref: "CHK-17", How: api.Current}, {Id: "tsk_two", Ref: "CHK-12", How: api.Named}}
	got := TextMessage(api.Message{About: &tags, Body: "unchanged"})
	if len(got.About) != 2 || got.About[0] != "CHK-17" || got.About[1] != "CHK-12" || got.Body != "unchanged" {
		t.Fatalf("mapped=%+v", got)
	}
}
