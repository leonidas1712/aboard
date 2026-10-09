package apiserver

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestLocationUsesOnlyItsIssuingSeatCredential(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPut || r.URL.Path != "/v1/me/location" || r.Header.Get("Authorization") != "Bearer seat-only" || r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("wrong location request: %s %s auth=%t keyed=%t", r.Method, r.URL.Path, r.Header.Get("Authorization") == "Bearer seat-only", r.Header.Get("Idempotency-Key") != "")
		}
		var body api.AgentLocationReport
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Folder != "/work/project" || body.SessionId != "root" || body.Harness != "codex" {
			t.Errorf("location body: %+v %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"harness":"codex","session_id":"root","folder":"/work/project","last_active":"2026-10-09T12:00:00Z"}`))
	}))
	defer remote.Close()
	ref := delivery.AgentRef{Server: remote.URL, Board: "work", Name: "reviewer", MemberID: "mem_selected"}
	server := New(remote.URL, tokens{human: map[string]string{remote.URL: "never-human"}, agents: map[delivery.AgentRef]string{ref: "seat-only"}}, rand.Reader)
	location := delivery.SessionLocation{Harness: "codex", SessionID: "root", Folder: "/work/project"}
	if err := server.ReportLocation(t.Context(), ref, location); err != nil {
		t.Fatal(err)
	}
	missing := ref
	missing.Name = "unknown"
	if err := server.ReportLocation(t.Context(), missing, location); err == nil {
		t.Fatal("missing seat used a fallback")
	}
	foreign := ref
	foreign.Server = "https://other.example"
	if err := server.ReportLocation(t.Context(), foreign, location); err == nil {
		t.Fatal("foreign issuer accepted")
	}
	if calls.Load() != 1 {
		t.Fatalf("sent %d requests, want only own seat", calls.Load())
	}
}
