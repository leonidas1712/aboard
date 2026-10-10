package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestTaskCreationUsesSelectedIdentity(t *testing.T) {
	for _, agent := range []bool{false, true} {
		name := "person"
		if agent {
			name = "agent"
		}
		t.Run(name, func(t *testing.T) {
			var start bool
			var received bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/info":
					_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "features": []string{"tasks"}})
				case "/v1/boards/payments-design/tasks":
					if r.Method != http.MethodPost {
						t.Errorf("method %s", r.Method)
						w.WriteHeader(405)
						return
					}
					wantAuth := "Bearer abh_person"
					if agent {
						wantAuth = "Bearer aba_agent"
					}
					if r.Header.Get("Authorization") != wantAuth {
						t.Error("creation used wrong identity")
					}
					if r.Header.Get("Idempotency-Key") == "" {
						t.Error("missing idempotency key")
					}
					var req struct {
						Start bool   `json:"start"`
						Title string `json:"title"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					start, received = req.Start, true
					if req.Title != "Rotate test key" {
						t.Error("title changed")
					}
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "tsk_test", "ref": "PAY-1", "board": "payments-design", "title": req.Title, "state": "open", "with": []any{}, "blocked_on": []any{}})
				case "/v1/me":
					_ = json.NewEncoder(w).Encode(map[string]any{"name": "claude", "kind": "agent", "current_task": nil})
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "brd_test", "name": "payments-design", "policy": map[string]any{"visibility": "board"}})
				}
			}))
			t.Cleanup(srv.Close)
			e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", Token: "aba_agent"})
			args := []string{"task", "new", "Rotate test key", "--board", "payments-design", "--json"}
			if agent {
				args = append(args, "--as", "claude")
			}
			r := e.run(args...)
			if r.code != 0 {
				t.Fatalf("task new exit%d: %s", r.code, r.stdout)
			}
			if !received || start != agent {
				t.Fatalf("creation received=%v start=%v, want start=%v", received, start, agent)
			}
		})
	}
}

func TestTaskReferenceSelectsOnlySessionSeats(t *testing.T) {
	var started atomic.Int32
	peer := func(board, ref, token string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v1/info" {
				_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "features": []string{"tasks"}})
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Error("credential reached a different issuing server")
				w.WriteHeader(401)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/start") {
				started.Add(1)
				if board != "beta" {
					t.Error("task started on wrong board")
				}
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("missing idempotency key")
				}
			}
			if strings.Contains(r.URL.Path, "/tasks/") {
				if !strings.Contains(r.URL.Path, ref) {
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found", "message": "Unavailable", "hint": "List tasks"}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "tsk_beta", "ref": ref, "board": board, "title": "Selected work", "state": "in_progress", "with": []any{}, "blocked_on": []any{}})
				return
			}
			if r.URL.Path == "/v1/me" {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "mem_" + board, "name": "codex", "kind": "agent"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"members": []any{}})
		}))
		t.Cleanup(s.Close)
		return s
	}
	first := peer("alpha", "ALPHA-1", "scratch_alpha")
	second := peer("beta", "BETA-1", "scratch_beta")
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unseated credential was probed")
		w.WriteHeader(500)
	}))
	t.Cleanup(trap.Close)
	e := lifecycleMachine(t, first.URL, "scratch_person", agentCredential{Server: first.URL, Board: "alpha", Name: "codex", MemberID: "mem_alpha", Token: "scratch_alpha"})
	e.env["ABOARD_SESSION"] = "claude-code:task-seat-test"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	for _, cred := range []agentCredential{{Server: second.URL, Board: "beta", Name: "codex", MemberID: "mem_beta", Token: "scratch_beta"}, {Server: trap.URL, Board: "hidden", Name: "codex", MemberID: "mem_hidden", Token: "scratch_hidden"}} {
		if err := a.saveCredential(cred); err != nil {
			t.Fatal(err)
		}
	}
	fakeDaemon(t, a, []delivery.AgentRef{{Server: first.URL, Board: "alpha", Name: "codex", MemberID: "mem_alpha"}, {Server: second.URL, Board: "beta", Name: "codex", MemberID: "mem_beta"}})
	r := e.run("task", "start", "BETA-1", "--json")
	if r.code != 0 {
		t.Fatalf("task start exit%d: %s", r.code, r.stdout)
	}
	var out struct {
		Board string `json:"board"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	if out.Board != "beta" || started.Load() != 1 {
		t.Fatalf("board=%s starts=%d", out.Board, started.Load())
	}
}

func TestTaskInboxAdviceStaysPerSeat(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"version": version, "features": []string{"tasks"}})
			return
		}
		if r.URL.Path != "/v1/me/inbox" {
			w.WriteHeader(404)
			return
		}
		name, id := "alpha", "mem_alpha"
		if r.Header.Get("Authorization") == "Bearer scratch_beta" {
			name, id = "beta", "mem_beta"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"board": "payments-design", "agent": name, "member_id": id, "messages": []any{}, "more": false, "work": map[string]any{"current_task": nil, "line": nil, "brief": nil, "asks_waiting": 0, "nudges": enabled.Load(), "open_tasks": 1, "posts_without_task": 0, "oldest_open": map[string]any{"id": "tsk_1", "ref": "PAY-1", "title": "Unclaimed work"}}})
	}))
	t.Cleanup(srv.Close)
	e := lifecycleMachine(t, srv.URL, "scratch_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "alpha", MemberID: "mem_alpha", Token: "scratch_alpha"})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "payments-design", Name: "beta", MemberID: "mem_beta", Token: "scratch_beta"}); err != nil {
		t.Fatal(err)
	}
	check := func(name string, want int) {
		t.Helper()
		r := e.run("inbox", "--as", name, "--peek", "--json")
		if r.code != 0 {
			t.Fatalf("inbox exit%d: %s", r.code, r.stdout)
		}
		var out struct {
			Nudges []struct {
				Code string `json:"code"`
			} `json:"nudges"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Nudges) != want {
			t.Fatalf("%s advice count%d want%d", name, len(out.Nudges), want)
		}
		if want > 0 && out.Nudges[0].Code != "tasks_not_picked_up" {
			t.Error("wrong reminder")
		}
	}
	check("alpha", 1)
	check("alpha", 0)
	check("beta", 1)
	enabled.Store(false)
	check("alpha", 0)
	check("beta", 0)
}
