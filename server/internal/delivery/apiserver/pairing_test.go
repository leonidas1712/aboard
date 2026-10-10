package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestPairingEndpointPrivateBeforeMintAndConfirmedOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	var p *Pairing
	var mu sync.Mutex
	var secret string
	verified := 0
	current := delivery.PairingRequest{ID: "prq_00000000000000000000000001", BoardID: "brd_00000000000000000000000001", Generation: 1, State: "awaiting_endpoint", Initiator: &delivery.PairingEndpoint{AgentID: "mem_own", Generation: 1, SessionBinding: "sha256:session"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/pairing-credentials":
			if r.Header.Get("Authorization") != "Bearer parent" {
				t.Errorf("mint used wrong authority")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			secret, _ = body["client_token"].(string)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Errorf("credential not saved before mint: %v", err)
			} else {
				raw, _ := os.ReadFile(filepath.Join(dir, entries[0].Name())) // #nosec G304 -- dir is this test's private temporary directory.
				if !strings.Contains(string(raw), secret) {
					t.Error("saved credential differs")
				}
				info, _ := entries[0].Info()
				if info.Mode().Perm() != 0o600 {
					t.Errorf("private mode %o", info.Mode().Perm())
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"request": current, "expires_at": time.Now().Add(10 * time.Minute)})
		case strings.HasSuffix(r.URL.Path, "/verify"):
			if r.Header.Get("Authorization") != "Bearer "+secret {
				t.Error("verify did not use private endpoint credential")
			}
			verified++
			current.State = "ready"
			_ = json.NewEncoder(w).Encode(current)
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]any{{"seq": 8, "reply_to_seq": 7, "body": pairingMarker(current.ID, 1, "initiator_to_recipient", "reply")}}})
		default:
			_ = json.NewEncoder(w).Encode(current)
		}
	}))
	defer server.Close()
	agent := delivery.AgentRef{Server: server.URL, Board: "work", MemberID: "mem_own", Name: "worker"}
	p = NewPairing(server.URL, tokens{human: map[string]string{server.URL: "parent"}, agents: map[delivery.AgentRef]string{agent: "seat"}}, dir)
	ctx := context.Background()
	got, err := p.Select(ctx, current, "initiator", agent, "sha256:session", false, "select")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "parent") || strings.Contains(string(raw), "seat") {
		t.Fatal("secret in returned metadata")
	}
	saved, err := p.read(current.ID, "initiator")
	if err != nil {
		t.Fatal(err)
	}
	saved.PingSeq = 7
	if err := p.save(saved); err != nil {
		t.Fatal(err)
	}
	current.State = "verifying"
	current.Recipient = &delivery.PairingEndpoint{AgentID: "mem_peer", Generation: 1, SessionBinding: "sha256:peer"}
	for _, bad := range []delivery.Delivery{{Agent: agent, State: delivery.StateHanded, HandoffID: "not-confirmed", Seqs: []int{8}}, {Agent: agent, State: delivery.StateConfirmed, Seqs: []int{8}}} {
		if _, err := p.Progress(ctx, current, agent, "sha256:session", []delivery.Delivery{bad}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	n := verified
	mu.Unlock()
	if n != 0 {
		t.Fatal("unconfirmed/history-only evidence verified")
	}
	if _, err := p.Progress(ctx, current, agent, "sha256:replacement", []delivery.Delivery{{Agent: agent, State: delivery.StateConfirmed, HandoffID: "handoff", Seqs: []int{8}}}); err == nil {
		t.Fatal("replacement accepted old private credential")
	}
	if _, err := p.Progress(ctx, current, agent, "sha256:session", []delivery.Delivery{{Agent: agent, State: delivery.StateConfirmed, HandoffID: "handoff", Seqs: []int{8}}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n = verified
	mu.Unlock()
	if n != 1 {
		t.Fatalf("verified=%d", n)
	}
}

func TestPairingRealAPIRequiresBothRuntimeConfirmedReplies(t *testing.T) {
	server, owner := localServer(t)
	ctx := context.Background()
	board := post(t, "POST", server+"/v1/boards", owner, map[string]any{"template": "general"})
	name, _ := board["name"].(string)
	boardID, _ := board["id"].(string)
	person, ok := post(t, "GET", server+"/v1/me", owner, nil)["id"].(string)
	if !ok {
		t.Fatal("current person response has no id")
	}
	joined := [2]delivery.SeatGrant{}
	agents := [2]delivery.AgentRef{}
	keys := tokens{human: map[string]string{server: owner}, agents: map[delivery.AgentRef]string{}}
	delegate := NewDelegated(server, "fixture", keys)
	for i := range 2 {
		g, err := delegate.Join(ctx, delivery.SeatRequest{Board: name, Name: fmt.Sprintf("worker%d", i), Harness: "codex", Session: fmt.Sprintf("codex:fixture%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		g.Seat.Server = server
		joined[i] = g
		agents[i] = delivery.AgentRef{Server: server, Board: name, MemberID: g.Seat.MemberID, Name: g.Seat.Name}
		keys.agents[agents[i]] = g.Token
	}
	runtimes := [2]*Pairing{NewPairing(server, keys, filepath.Join(t.TempDir(), "endpoint")), NewPairing(server, keys, filepath.Join(t.TempDir(), "endpoint"))}
	request, err := runtimes[0].Create(ctx, delivery.PairingCreate{BoardID: boardID, RecipientID: person, InitiatingAgentID: agents[0].MemberID, Work: "Review together."}, agents[0], "request")
	if err != nil {
		t.Fatal(err)
	}
	bindings := [2]string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64)}
	request, err = runtimes[0].Select(ctx, request, "initiator", agents[0], bindings[0], false, "select0")
	if err != nil {
		t.Fatal(err)
	}
	request, err = runtimes[1].Select(ctx, request, "recipient", agents[1], bindings[1], false, "select1")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		request, err = runtimes[i].Progress(ctx, request, agents[i], bindings[i], nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if request.State != "verifying" {
		t.Fatalf("premature ready %s", request.State)
	}
	for i, side := range []string{"initiator", "recipient"} {
		saved, err := runtimes[i].read(request.ID, side)
		if err != nil || saved.PingSeq == 0 {
			t.Fatalf("ping was not posted: %v", err)
		}
		peer := 1 - i
		replied := post(t, "POST", server+"/v1/boards/"+name+"/messages", joined[peer].Token, map[string]any{"body": pairingMarker(request.ID, 1, pairingDirection(side), "reply"), "to": []string{"@" + agents[i].Name}, "reply_to": saved.PingID})
		seq, ok := replied["seq"].(float64)
		if !ok {
			t.Fatalf("reply failed: %v", replied)
		}
		request, err = runtimes[i].Progress(ctx, request, agents[i], bindings[i], nil)
		if err != nil {
			t.Fatal(err)
		}
		if request.State != "verifying" {
			t.Fatal("history alone became ready")
		}
		request, err = runtimes[i].Progress(ctx, request, agents[i], bindings[i], []delivery.Delivery{{Agent: agents[i], State: delivery.StateConfirmed, HandoffID: fmt.Sprintf("confirmed-%d", i), Seqs: []int{int(seq)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if request.State != "ready" {
		t.Fatalf("both confirmed round trips: %s", request.State)
	}
}

func TestPairingUnsafePrivateDirectoryRefusesBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(root, "linked")
	if err := os.Symlink(target, unsafe); err != nil {
		t.Fatal(err)
	}
	agent := delivery.AgentRef{Server: server.URL, Board: "work", MemberID: "own", Name: "worker"}
	p := NewPairing(server.URL, tokens{human: map[string]string{server.URL: "parent"}}, unsafe)
	if _, err := p.Select(context.Background(), delivery.PairingRequest{ID: "prq_1", Generation: 1}, "initiator", agent, "sha256:binding", false, "key"); err == nil {
		t.Fatal("symlinked private directory accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe save sent a parent credential over HTTP")
	}
	if err := os.Remove(unsafe); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(unsafe, 0o755); err != nil { // #nosec G301 -- deliberately unsafe mode exercises refusal.
		t.Fatal(err)
	}
	if _, err := p.Select(context.Background(), delivery.PairingRequest{ID: "prq_1", Generation: 1}, "initiator", agent, "sha256:binding", false, "key"); err == nil {
		t.Fatal("open private directory accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe save sent HTTP")
	}
}

type lostPairingMint struct{ drop atomic.Bool }

func (l *lostPairingMint) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(req)
	if err == nil && req.URL.Path == "/v1/pairing-credentials" && l.drop.Swap(false) {
		_ = response.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return response, err
}

func TestPairingLostReplacementAnswerReusesDurableTokenAndOriginalCAS(t *testing.T) {
	server, owner := localServer(t)
	ctx := context.Background()
	board := post(t, "POST", server+"/v1/boards", owner, map[string]any{"template": "general"})
	name, _ := board["name"].(string)
	id, _ := board["id"].(string)
	person, ok := post(t, "GET", server+"/v1/me", owner, nil)["id"].(string)
	if !ok {
		t.Fatal("no person")
	}
	keys := tokens{human: map[string]string{server: owner}, agents: map[delivery.AgentRef]string{}}
	grant, err := NewDelegated(server, "fixture", keys).Join(ctx, delivery.SeatRequest{Board: name, Harness: "codex", Session: "codex:original"})
	if err != nil {
		t.Fatal(err)
	}
	agent := delivery.AgentRef{Server: server, Board: name, Name: grant.Seat.Name, MemberID: grant.Seat.MemberID}
	keys.agents[agent] = grant.Token
	dir := filepath.Join(t.TempDir(), "endpoint")
	p := NewPairing(server, keys, dir)
	request, err := p.Create(ctx, delivery.PairingCreate{BoardID: id, RecipientID: person, InitiatingAgentID: agent.MemberID, Work: "Pair."}, agent, "request")
	if err != nil {
		t.Fatal(err)
	}
	request, err = p.Select(ctx, request, "initiator", agent, "sha256:"+strings.Repeat("a", 64), false, "initial")
	if err != nil {
		t.Fatal(err)
	}
	lost := &lostPairingMint{}
	lost.drop.Store(true)
	p.remote.http.Transport = lost
	binding := "sha256:" + strings.Repeat("b", 64)
	if _, err = p.Select(ctx, request, "initiator", agent, binding, true, "replace"); err == nil {
		t.Fatal("committed answer was not lost")
	}
	pending, err := p.read(request.ID, "initiator")
	if err != nil || !pending.PendingMint || pending.MintGeneration != 1 {
		t.Fatalf("pending recovery missing: %v", err)
	}
	restarted := NewPairing(server, keys, dir)
	fresh, err := restarted.Get(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation != 2 {
		t.Fatalf("first replace generation=%d", fresh.Generation)
	}
	recovered, err := restarted.Select(ctx, fresh, "initiator", agent, binding, true, "deliberate-new-control-call")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := restarted.read(request.ID, "initiator")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Generation != 2 || saved.Token != pending.Token || saved.PendingMint {
		t.Fatal("retry replaced again or lost the original private proof")
	}
}
