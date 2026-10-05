package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func seatApp(t *testing.T) *app {
	t.Helper()
	home := t.TempDir()
	return &app{env: Env{Rand: rand.Reader, Getenv: func(k string) string {
		if k == "ABOARD_HOME" || k == "HOME" {
			return home
		}
		if k == "ABOARD_LOCAL_ADDR" {
			return "127.0.0.1:1"
		}
		return ""
	}}}
}

func TestSameNameSeatsKeepSeparateCredentials(t *testing.T) {
	a := seatApp(t)
	for _, id := range []string{"mem_old", "mem_new"} {
		if err := a.saveCredential(agentCredential{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: id, Token: "aba_" + id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"mem_old", "mem_new"} {
		got, err := (daemonTokens{a}).AgentToken(delivery.AgentRef{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: id})
		if err != nil || got != "aba_"+id {
			t.Fatalf("seat %s: token %q, error %v", id, got, err)
		}
	}
	if token, err := (daemonTokens{a}).AgentToken(delivery.AgentRef{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_missing"}); err == nil || token != "" {
		t.Fatalf("unknown seat got %q, %v", token, err)
	}
}

func TestLegacyPromotionRequiresTheSameToken(t *testing.T) {
	c := credentials{Agents: []agentCredential{{Server: "s", Board: "b", Name: "writer", Token: "aba_old"}}}
	c.put(agentCredential{Server: "s", Board: "b", Name: "writer", MemberID: "mem_new", Token: "aba_new"})
	if len(c.Agents) != 2 {
		t.Fatal("new seat replaced an unverified legacy token")
	}
	c.put(agentCredential{Server: "s", Board: "b", Name: "writer", MemberID: "mem_old", Token: "aba_old"})
	if len(c.Agents) != 2 || c.Agents[0].MemberID != "mem_old" {
		t.Fatalf("legacy promotion: %+v", c.Agents)
	}
}

func TestSavedTokenProvesItsSeatBeforeLegacyBackfill(t *testing.T) {
	for _, tc := range []struct {
		name, kind, id, gotName string
		status                  int
		want                    bool
	}{
		{"legacy", "agent", "mem_old", "writer", 200, true},
		{"wrong kind", "human", "hum_owner", "writer", 200, false},
		{"legacy name reused", "agent", "mem_new", "replacement", 200, false},
		{"revoked", "agent", "mem_old", "writer", 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/me" || r.Header.Get("Authorization") != "Bearer aba_old" {
					t.Errorf("unexpected request %s, token %q", r.URL.Path, r.Header.Get("Authorization"))
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					_ = json.NewEncoder(w).Encode(api.Me{Id: tc.id, Kind: api.MeKind(tc.kind), Name: tc.gotName, Board: ptr("docs")})
				}
			}))
			defer srv.Close()
			a := seatApp(t)
			if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", Token: "aba_old"}); err != nil {
				t.Fatal(err)
			}
			ref, err := (daemonTokens{a}).ResolveAgent(t.Context(), delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer"})
			if (err == nil) != tc.want {
				t.Fatalf("resolved %+v: %v", ref, err)
			}
			c, err := a.readCredentials()
			if err != nil {
				t.Fatal(err)
			}
			if tc.want && (ref.MemberID != "mem_old" || c.Agents[0].MemberID != "mem_old") {
				t.Fatalf("not backfilled: %+v %+v", ref, c)
			}
			if !tc.want && c.Agents[0].MemberID != "" {
				t.Fatal("unverified identity persisted")
			}
			if requests.Load() != 1 {
				t.Fatalf("requests: %d", requests.Load())
			}
		})
	}
}

func TestSeatResolutionNeverFollowsRedirectOrUsesAnotherSeat(t *testing.T) {
	var leaked atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { leaked.Add(1) }))
	defer elsewhere.Close()
	var issuerRequests atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuerRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Errorf("wrong token: %q", r.Header.Get("Authorization"))
		}
		http.Redirect(w, r, elsewhere.URL+"/v1/me", http.StatusTemporaryRedirect)
	}))
	defer issuer.Close()
	a := seatApp(t)
	if err := a.saveCredential(agentCredential{Server: issuer.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"}); err != nil {
		t.Fatal(err)
	}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.servers(), serverLogins{Servers: []serverLogin{{URL: issuer.URL, Handle: "owner", Key: "abh_person"}}}, 0o600); err != nil {
		t.Fatal(err)
	}
	tokens := daemonTokens{a}
	if _, err := tokens.ResolveAgent(t.Context(), delivery.AgentRef{Server: issuer.URL, Board: "docs", Name: "writer", MemberID: "mem_missing"}); err == nil {
		t.Fatal("missing ID used another token")
	}
	if issuerRequests.Load() != 0 {
		t.Fatal("missing seat sent a request with another credential")
	}
	if _, err := tokens.ResolveAgent(t.Context(), delivery.AgentRef{Server: issuer.URL, Board: "docs", Name: "writer", MemberID: "mem_old"}); err == nil {
		t.Fatal("redirect resolved a seat")
	}
	if leaked.Load() != 0 {
		t.Fatal("token followed redirect")
	}
}

func TestKnownSeatVerifiesItsIdAndRefreshesOnlyItsName(t *testing.T) {
	for _, id := range []string{"mem_old", "mem_other"} {
		t.Run(id, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(api.Me{Id: id, Kind: api.MeKindAgent, Name: "renamed", Board: ptr("docs")})
			}))
			defer srv.Close()
			a := seatApp(t)
			if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"}); err != nil {
				t.Fatal(err)
			}
			ref, err := (daemonTokens{a}).ResolveAgent(t.Context(), delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old"})
			if id == "mem_old" {
				if err != nil || ref.Name != "renamed" || ref.MemberID != id {
					t.Fatalf("rename: %+v %v", ref, err)
				}
				token, err := (daemonTokens{a}).AgentToken(delivery.AgentRef{Server: srv.URL, MemberID: id, Name: "old name"})
				if err != nil || token != "aba_old" {
					t.Fatalf("renamed token: %q %v", token, err)
				}
			} else if err == nil {
				t.Fatal("token's different ID was accepted")
			}
		})
	}
}

func TestResolvingASeatDoesNotOverwriteARotatedToken(t *testing.T) {
	requested, finish := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requested)
		<-finish
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Me{Id: "mem_old", Kind: api.MeKindAgent, Name: "writer", Board: ptr("docs")})
	}))
	defer srv.Close()
	a := seatApp(t)
	cred := agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"}
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (daemonTokens{a}).ResolveAgent(t.Context(), delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old"})
		done <- err
	}()
	<-requested
	cred.Token = "aba_rotated"
	err := a.saveCredential(cred)
	close(finish)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("old response accepted after token rotation")
	}
	got, err := (daemonTokens{a}).AgentToken(delivery.AgentRef{Server: srv.URL, MemberID: "mem_old"})
	if err != nil || got != "aba_rotated" {
		t.Fatalf("token rotation lost: %q %v", got, err)
	}
}

func TestLegacySaveCannotReplaceAnUnverifiedIdentity(t *testing.T) {
	c := credentials{Agents: []agentCredential{{Server: "s", Board: "b", Name: "writer", Token: "aba_old"}}}
	c.put(agentCredential{Server: "s", Board: "b", Name: "writer", Token: "aba_new"})
	if len(c.Agents) != 2 || c.Agents[0].Token != "aba_old" {
		t.Fatal("legacy save replaced another token without proving its identity")
	}
	if _, ok := c.forSeat(delivery.AgentRef{Server: "s", Board: "b", Name: "writer"}); ok {
		t.Fatal("ambiguous legacy credentials selected by name")
	}
}

func TestAnUnresolvedLegacyReferenceNeverUsesAFreshSameNameSeat(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Me{Id: "mem_new", Kind: api.MeKindAgent, Name: "writer", Board: ptr("docs")})
	}))
	defer srv.Close()
	a := seatApp(t)
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"}); err != nil {
		t.Fatal(err)
	}
	old := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer"}
	if _, err := (daemonTokens{a}).ResolveAgent(t.Context(), old); err == nil {
		t.Fatal("unresolved old reference adopted fresh same-name seat")
	}
	if requests.Load() != 0 {
		t.Fatal("fresh replacement token sent for unresolved old reference")
	}
}

func TestLegacyBackfillSurvivesRestartAndSameNameReplacement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := "mem_old"
		if r.Header.Get("Authorization") == "Bearer aba_new" {
			id = "mem_new"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Me{Id: id, Kind: api.MeKindAgent, Name: "writer", Board: ptr("docs")})
	}))
	defer srv.Close()
	a := seatApp(t)
	cred := agentCredential{Server: srv.URL, Board: "docs", Name: "writer", Token: "aba_old"}
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	old := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer"}
	if ref, err := (daemonTokens{a}).ResolveAgent(t.Context(), old); err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("first backfill: %+v %v", ref, err)
	}
	// Restart before the caller can commit the resolved id to its journal.
	restarted := &app{env: a.env}
	if err := restarted.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"}); err != nil {
		t.Fatal(err)
	}
	ref, err := (daemonTokens{restarted}).ResolveAgent(t.Context(), old)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("legacy proof lost across restart/replacement: %+v %v", ref, err)
	}
	cred.MemberID, cred.Token = "mem_old", "aba_rotated"
	if err := restarted.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	ref, err = (daemonTokens{restarted}).ResolveAgent(t.Context(), old)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("legacy proof lost across own-id rotation: %+v %v", ref, err)
	}
}

func TestAnUnavailableIdentityServerDoesNotEndASavedSeat(t *testing.T) {
	for _, id := range []string{"", "mem_old"} {
		t.Run("member_id="+id, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"code":"server_unavailable","message":"Try again.","hint":"Try later."}}`))
			}))
			defer srv.Close()
			a := seatApp(t)
			if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: id, Token: "aba_old"}); err != nil {
				t.Fatal(err)
			}
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(p.credentials())
			if err != nil {
				t.Fatal(err)
			}
			ref, err := (daemonTokens{a}).ResolveAgent(t.Context(), delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer", MemberID: id})
			if err == nil || errors.Is(err, delivery.ErrUnauthorized) {
				t.Fatalf("temporary outage classified as ended identity: %+v %v", ref, err)
			}
			if ref.MemberID != "" {
				t.Fatal("temporary outage inferred identity")
			}
			after, err := os.ReadFile(p.credentials())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("temporary outage changed credentials")
			}
		})
	}
}
