package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func d223SpecialApp(t *testing.T, first, second string, out *bytes.Buffer) (*app, []delivery.AgentRef) {
	t.Helper()
	e := lifecycleMachine(t, first, "never-human", agentCredential{Server: first, Board: "work", Name: "scout", MemberID: "mem_a", Token: "seat-a"})
	e.env["ABOARD_SESSION"] = "claude-code:original"
	p, err := resolvePaths(e.getenv)
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials{Agents: []agentCredential{{Server: first, Board: "work", Name: "scout", MemberID: "mem_a", Token: "seat-a"}, {Server: second, Board: "work", Name: "scout", MemberID: "mem_b", Token: "seat-b"}}}
	if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &app{env: e.environment(out, out), json: true, localChecked: true}
	return a, []delivery.AgentRef{{Server: first, Board: "work", Name: "scout", MemberID: "mem_a"}, {Server: second, Board: "work", Name: "scout", MemberID: "mem_b"}}
}

func TestD223PersonInviteSelectsExplicitIssuerWithIdenticalSeats(t *testing.T) {
	var callsA, issued atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { callsA.Add(1); w.WriteHeader(500) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seat-b" {
			t.Error("wrong issuer credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/boards/work":
			_, _ = w.Write([]byte(`{"id":"brd_b","name":"work"}`))
		case "/v1/me/admin-requests":
			issued.Add(1)
			var payload struct {
				Invite struct {
					Boards []string `json:"boards"`
					TTL    int      `json:"ttl_seconds"`
				} `json:"invite"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if len(payload.Invite.Boards) != 1 || payload.Invite.Boards[0] != "brd_b" || payload.Invite.TTL != 7200 {
				t.Errorf("wrong exact payload: %+v", payload)
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"state":"pending","approval":{"id":"apr_b","state":"pending","agent_id":"mem_b"},"next":{"command":"aboard approvals allow apr_b","resume":"Continue after approval."}}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer second.Close()
	var out bytes.Buffer
	a, seats := d223SpecialApp(t, first.URL, second.URL, &out)
	fakeDaemon(t, a, seats)
	err := runInvite(context.Background(), a, []string{"--person", "--board", "work", "--server", second.URL, "--ttl", "2h"})
	if err != nil {
		t.Fatal(err)
	}
	if callsA.Load() != 0 || issued.Load() != 1 || !strings.Contains(out.String(), second.URL) {
		t.Fatalf("wrong route A=%d issued=%d output=%s", callsA.Load(), issued.Load(), out.String())
	}
}

func TestD223OrdinaryInviteHandoffKeepsIssuerAndTTL(t *testing.T) {
	var out bytes.Buffer
	a, _ := d223SpecialApp(t, "https://one.example", "https://two.example", &out)
	err := runInvite(context.Background(), a, []string{"--board", "work", "--server", "https://two.example", "--ttl", "2h"})
	if err == nil || asError(err).Code != "human_command_in_session" {
		t.Fatalf("expected handoff: %v", err)
	}
	hint := asError(err).Hint
	for _, want := range []string{"--board work", "--server https://two.example", "--ttl 2h0m0s"} {
		if !strings.Contains(hint, want) {
			t.Errorf("handoff lost %q: %s", want, hint)
		}
	}
}

func TestD223PairingFiltersExplicitIssuerAndRefusesImplicit(t *testing.T) {
	var callsA, readsB, writes atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { callsA.Add(1); w.WriteHeader(500) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readsB.Add(1)
		if r.Header.Get("Authorization") != "Bearer seat-b" {
			t.Error("pairing used wrong credential")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/boards/work":
			_, _ = w.Write([]byte(`{"id":"brd_b","name":"work"}`))
		case "/v1/me":
			_, _ = w.Write([]byte(`{"id":"mem_b","name":"scout","kind":"agent","board":"work","owner":"alex"}`))
		case "/v1/people":
			_, _ = w.Write([]byte(`{"id":"hum_other","handle":"other"}`))
		default:
			t.Errorf("unexpected pairing route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer second.Close()
	var out bytes.Buffer
	a, seats := d223SpecialApp(t, first.URL, second.URL, &out)
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		resp := delivery.Response{V: delivery.ProtocolVersion}
		if req.Op == delivery.OpAgents {
			resp.Agents = seats
		}
		if req.Op == delivery.OpPairing {
			writes.Add(1)
			if req.Server != second.URL || req.BoardID != "brd_b" || req.InitiatingAgentID != "mem_b" {
				t.Errorf("wrong pairing request: %+v", req)
			}
			resp.Pairing = &delivery.PairingRequest{ID: "prq_b", ServerID: "srv_b", BoardID: "brd_b", State: "pending", Generation: 1, CreatedAt: "2026-10-10T00:00:00Z", ExpiresAt: "2026-10-11T00:00:00Z"}
		}
		return resp
	})
	t.Run("explicit", func(t *testing.T) {
		err := runPairing(context.Background(), a, []string{"request", "@other", "Review work", "--board", "work", "--server", second.URL})
		if err != nil {
			t.Fatal(err)
		}
		if callsA.Load() != 0 || readsB.Load() != 3 || writes.Load() != 1 {
			t.Fatalf("routing A=%d B=%d writes=%d", callsA.Load(), readsB.Load(), writes.Load())
		}
	})
	t.Run("implicit", func(t *testing.T) {
		before := writes.Load()
		err := runPairing(context.Background(), a, []string{"list"})
		if err == nil || asError(err).Code != "server_not_selected" || writes.Load() != before {
			t.Fatalf("implicit multiissuer pairing: %v writes=%d", err, writes.Load())
		}
	})
}

func TestD223SessionBoardsHintsKeepExplicitIssuer(t *testing.T) {
	var out bytes.Buffer
	a, seats := d223SpecialApp(t, "https://one.example", "https://two.example", &out)
	a.json = false
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		resp := delivery.Response{V: delivery.ProtocolVersion}
		if req.Op == delivery.OpAgents {
			resp.Agents = seats[:1]
		}
		if req.Op == delivery.OpBoards {
			if req.Server != "https://two.example" {
				t.Errorf("listed wrong issuer %s", req.Server)
			}
			resp.Server = req.Server
			resp.Boards = []json.RawMessage{json.RawMessage(`{"name":"work","visibility":"open","on_board":false}`)}
		}
		return resp
	})
	if err := runBoards(context.Background(), a, []string{"--server", "https://two.example"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "aboard join --board work --server https://two.example") && !strings.Contains(out.String(), "aboard join --server https://two.example --board work") {
		t.Fatalf("hint dropped selected issuer: %s", out.String())
	}
}
