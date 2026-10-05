package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// SeatID names the exact seat an agent carries the member id of, never one found by
// its board and name: two seats can share a name over time.
func TestSeatIDIsTheExactSeat(t *testing.T) {
	const srv = "http://127.0.0.1:7400"
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Getenv: func(k string) string {
		return map[string]string{"ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:7400"}[k]
	}}}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	old := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1A"
	now := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1B"
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "docs", Name: "claude", MemberID: old, Token: "t1"},
		{Server: srv, Board: "docs", Name: "claude", MemberID: now, Token: "t2"},
	}}
	if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newDaemonSeats(a)
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude", MemberID: now}); !ok || id != now {
		t.Errorf("by member id: %q %v", id, ok)
	}
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude"}); ok {
		t.Errorf("by name: %q", id)
	}
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1C"}); ok {
		t.Errorf("an unknown member id: %q", id)
	}
}

// A replacement seat with an old seat's name never fills the old seat's row: the seat's
// credential and member are found by its member id, and read with that seat's token.
func TestASameNameReplacementNeverFillsTheOldSeatsRow(t *testing.T) {
	old, now := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1A", "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1B"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer aba_now" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"no","hint":"no"}}`))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/members"):
			_ = json.NewEncoder(w).Encode(map[string]any{"members": []map[string]any{{
				"id": now, "board": "docs", "name": "claude", "kind": "agent", "role": "member", "owner": "maya",
				"status": "active", "joined_at": "2026-10-01T16:00:00Z", "delivery_mode": "all", "delivery_revision": 1,
			}}})
		case r.URL.Path == "/v1/boards":
			_ = json.NewEncoder(w).Encode(map[string]any{"boards": []map[string]any{{"name": "docs", "unread": 4}}})
		}
	}))
	defer srv.Close()
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Getenv: func(k string) string {
		return map[string]string{"ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:1"}[k]
	}}}
	creds := credentials{Agents: []agentCredential{
		{Server: srv.URL, Board: "docs", Name: "claude", MemberID: old, Token: "aba_old"},
		{Server: srv.URL, Board: "docs", Name: "claude", MemberID: now, Token: "aba_now"},
	}}
	if c, ok := seatCredential(creds, srv.URL, now); !ok || c.Token != "aba_now" {
		t.Fatalf("the seat's credential: %+v %v", c, ok)
	}
	rows := a.sessionSeats(context.Background(), []delivery.AgentRef{{Server: srv.URL, Board: "docs", Name: "claude", MemberID: now}}, creds)
	if len(rows) != 1 || rows[0].MemberID != now || rows[0].Unread == nil || *rows[0].Unread != 4 || rows[0].Delivery != "all" {
		t.Fatalf("rows: %+v", rows)
	}
}
