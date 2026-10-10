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

func TestQueuedInboxAndStatusKeepEqualIdentitiesOnSelectedIssuer(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { firstCalls.Add(1); w.WriteHeader(500) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer seat-b" {
			t.Error("queue preview used a foreign credential")
		}
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/me/inbox":
			_, _ = w.Write([]byte(`{"board":"work","board_id":"brd_same","member_id":"mem_same","cursor":0,"messages":[]}`))
		case "/v1/boards/work/messages":
			_, _ = w.Write([]byte(`{"messages":[{"id":"msg_same","board":"work","seq":7,"from":{"kind":"human","name":"pat"},"body":"issuer B queued body","expects_reply":true}]}`))
		default:
			t.Errorf("unexpected queue preview request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer second.Close()
	e := lifecycleMachine(t, first.URL, "never-human", agentCredential{})
	e.env["ABOARD_SESSION"] = "codex:shared"
	var stdout bytes.Buffer
	a := e.app(&stdout, &bytes.Buffer{})
	a.json = true
	a.localChecked = true
	creds := credentials{Agents: []agentCredential{{Server: first.URL, Board: "work", Name: "writer", MemberID: "mem_same", Token: "seat-a"}, {Server: second.URL, Board: "work", Name: "writer", MemberID: "mem_same", Token: "seat-b"}}}
	p, _ := a.paths()
	if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"agents": []delivery.AgentRef{{Server: first.URL, Board: "work", Name: "writer", MemberID: "mem_same"}, {Server: second.URL, Board: "work", Name: "writer", MemberID: "mem_same"}}, "queued": map[string]any{"count": 2, "messages": []any{map[string]any{"server": first.URL, "board": "work", "board_id": "brd_same", "member_id": "mem_same", "message_id": "msg_same", "seq": 7, "from": "@pat", "boundary": "turn_end"}, map[string]any{"server": second.URL, "board": "work", "board_id": "brd_same", "member_id": "mem_same", "message_id": "msg_same", "seq": 7, "from": "@pat", "boundary": "turn_end"}}}})
	var observation delivery.Response
	if err := json.Unmarshal(raw, &observation); err != nil {
		t.Fatal(err)
	}
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		if req.Server != second.URL {
			t.Error("queue observation did not filter issuer before daemon reads")
		}
		return observation
	})
	if err := runInbox(context.Background(), a, []string{"--queued", "--server", second.URL, "--board", "work"}); err != nil {
		t.Fatalf("selected queue: %v", err)
	}
	if firstCalls.Load() != 0 || secondCalls.Load() != 2 {
		t.Fatalf("queue contacted wrong issuer A=%d B=%d", firstCalls.Load(), secondCalls.Load())
	}
	var out struct {
		Queued  delivery.QueuedMessages `json:"queued"`
		Wrapped []string                `json:"wrapped"`
		Bundle  string                  `json:"bundle"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Queued.Count != 1 || len(out.Wrapped) != 1 || !strings.Contains(out.Wrapped[0], `server="`+second.URL+`"`) || !strings.Contains(out.Bundle, "--server "+second.URL) {
		t.Fatalf("queue identity or rendered reply lost issuer: %s", stdout.String())
	}
	q := a.statusQueued(context.Background(), second.URL, "mem_same")
	if q == nil || q.Count != 1 {
		t.Fatalf("selected status mixed equal members: %+v", q)
	}
	if firstCalls.Load() != 0 || secondCalls.Load() != 2 {
		t.Fatal("status observation made authenticated REST requests")
	}
}

func TestQueuedLegacyIdentityRequiresOneObservedIssuer(t *testing.T) {
	refs := []delivery.AgentRef{{Server: "https://a.example", Board: "work", MemberID: "mem_same"}, {Server: "https://b.example", Board: "work", MemberID: "mem_same"}}
	observation := delivery.Response{Agents: refs, Queued: &delivery.QueuedMessages{Count: 1, Messages: []delivery.QueuedMessage{{BoardID: "brd_same", MemberID: "mem_same", MessageID: "msg_same", Seq: 7}}}}
	if _, err := normalizeQueuedObservation(observation); err == nil {
		t.Fatal("issuer-less identity guessed among equal foreign seats")
	}
	observation.Agents = refs[1:]
	got, err := normalizeQueuedObservation(observation)
	if err != nil {
		t.Fatal(err)
	}
	if got.Queued.Messages[0].Server != refs[1].Server || got.Queued.Messages[0].Board != "work" {
		t.Fatalf("single-issuer legacy queue: %+v", got.Queued)
	}
	if observation.Queued.Messages[0].Server != "" {
		t.Fatal("normalization mutated original evidence")
	}
}
