package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSwarmChecksItsRecordedSeatWithoutUsingAReusedNamesToken(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Error("swarm sent a replacement seat's credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"ended","hint":"join"}}`))
	}))
	defer srv.Close()
	a := seatApp(t)
	for _, c := range []agentCredential{
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"},
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"},
	} {
		if err := a.saveCredential(c); err != nil {
			t.Fatal(err)
		}
	}
	rows := []swarmAgent{{Name: "writer", memberID: "mem_old"}}
	a.checkSeats(context.Background(), a.serverRefFor(srv.URL), "docs", rows)
	if requests.Load() != 1 || deref(rows[0].SeatCredential) != seatEnded {
		t.Fatalf("recorded seat check: %+v, %d requests", rows, requests.Load())
	}
}

func TestSwarmDoesNotShowAReplacementSeatsPresence(t *testing.T) {
	a := seatApp(t)
	presence := api.MemberPresenceWorking
	got := a.swarmRow("writer", &swarmAgentRecord{MemberID: "mem_old", Mode: "headless"}, "running",
		delivery.BindingStatus{}, map[string]api.Member{"writer": {Id: "mem_new", Name: "writer", Presence: &presence}})
	if got.Presence != nil || got.Seated {
		t.Fatalf("replacement presence attached to old record: %+v", got)
	}
}

func TestSwarmSelectsItsRecordedIdBeforeTheSharedDisplayName(t *testing.T) {
	a := seatApp(t)
	creds := credentials{Agents: []agentCredential{
		{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"},
		{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"},
	}}
	in := upInput{
		srv: serverRef{URL: "https://team.example"}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: creds,
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {MemberID: "mem_old", Handle: "old-session"}}},
	}
	ref, err := a.swarmSeat(t.Context(), in)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("recorded seat not selected: %+v %v", ref, err)
	}
}

func TestLegacySwarmCannotAdoptUnrelatedBackfillProvenance(t *testing.T) {
	a := seatApp(t)
	cred := agentCredential{
		Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new",
		Legacy: &legacyCredentialIdentity{Board: "docs", Name: "different-old-name"},
	}
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	in := upInput{
		srv: serverRef{URL: cred.Server}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: credentials{Agents: []agentCredential{cred}},
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {Handle: "old-session"}}},
	}
	if ref, err := a.swarmSeat(t.Context(), in); err == nil {
		t.Fatalf("unrelated provenance adopted old session: %+v", ref)
	}
}

func TestUnresolvedSwarmDoesNotShowAReplacementsServerMetadata(t *testing.T) {
	a := seatApp(t)
	presence := api.MemberPresenceWorking
	mode := api.MemberDeliveryModeAll
	got := a.swarmRow("writer", &swarmAgentRecord{Mode: "headless"}, "running", delivery.BindingStatus{},
		map[string]api.Member{"writer": {Id: "mem_new", Name: "writer", Presence: &presence, DeliveryMode: &mode}})
	if got.Presence != nil || got.Delivery != nil || got.Seated {
		t.Fatalf("unresolved legacy row adopted replacement metadata: %+v", got)
	}
}

func TestLegacySwarmResumesOnlyItsVerifiedOriginalToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Errorf("replacement token used: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Me{Id: "mem_old", Kind: api.MeKindAgent, Name: "writer", Board: ptr("docs")})
	}))
	defer srv.Close()
	a := seatApp(t)
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", Token: "aba_old"}); err != nil {
		t.Fatal(err)
	}
	original := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer"}
	if _, err := (daemonTokens{a}).ResolveAgent(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"}); err != nil {
		t.Fatal(err)
	}
	creds, err := a.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	in := upInput{
		srv: serverRef{URL: srv.URL}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: creds,
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {Handle: "old-session"}}},
	}
	ref, err := a.swarmSeat(t.Context(), in)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("verified original seat not resumed: %+v %v", ref, err)
	}
}
