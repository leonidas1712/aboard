package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func heldInvite(t *testing.T, s *testServer, agent string) string {
	t.Helper()
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	var out struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 202 || out.Approval.ID == "" {
		t.Fatalf("hold invite: %d %s %v", status, raw, err)
	}
	return "/v1/me/approvals/" + out.Approval.ID
}

func TestBrowserApprovedInviteCanBeCollectedOnceAfterRestart(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	page := s.cookieBrowser(s.owner)
	allowed := page.do("POST", path+"/allow", map[string]any{})
	if allowed.status != 200 {
		t.Fatalf("browser allow: %d %s", allowed.status, allowed.raw)
	}
	var execution struct {
		Invite struct {
			Invite string `json:"invite"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(allowed.raw), &execution); err != nil || execution.Invite.Invite == "" {
		t.Fatalf("missing approver invite: %s %v", allowed.raw, err)
	}
	s.restart()
	status, raw := onboardingCall(t, s, "GET", path, "", agent)
	if status != 200 || strings.Contains(raw, "abi_") {
		t.Fatalf("metadata: %d %s", status, raw)
	}
	collect := func() call {
		return s.send("POST", path+"/collect", nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+agent)
			r.Header.Set("Idempotency-Key", "same-collection")
		})
	}
	first := collect()
	if first.status != 200 || first.body["collected"] != true || !strings.Contains(string(first.raw), execution.Invite.Invite) {
		t.Fatalf("first collection: %d %s", first.status, first.raw)
	}
	s.restart()
	repeat := collect()
	if repeat.status != 200 || repeat.body["collected"] != false || strings.Contains(string(repeat.raw), "abi_") {
		t.Fatalf("repeat collection: %d %s", repeat.status, repeat.raw)
	}
	mustStatus(t, s.connect(execution.Invite.Invite, "collected"), nil, 201)
}

func TestConcurrentApprovalCollectorsHaveOneSecretBearingWinner(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	var wg sync.WaitGroup
	results := make(chan call, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.send("POST", path+"/collect", nil, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+agent)
				r.Header.Set("Idempotency-Key", "concurrent")
			})
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for c := range results {
		if c.status != 200 {
			t.Fatalf("collect: %d %s", c.status, c.raw)
		}
		if c.body["collected"] == true {
			winners++
			if !strings.Contains(string(c.raw), "abi_") {
				t.Fatal("winner missing invite")
			}
		} else if strings.Contains(string(c.raw), "abi_") {
			t.Fatal("repeat exposed invite")
		}
	}
	if winners != 1 {
		t.Fatalf("secret-bearing winners: %d", winners)
	}
}

func TestApprovalCollectionRefusesOtherCredentialKindsAndSeats(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, sibling := s.pair("starter")
	path := heldInvite(t, s, agent)
	status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	foreign := s.addHuman("foreign")
	for name, token := range map[string]string{"person": s.owner, "sibling": sibling, "foreign": foreign, "delegation": s.delegate(s.owner)} {
		t.Run(name, func(t *testing.T) {
			status, raw := onboardingCall(t, s, "POST", path+"/collect", `{}`, token)
			if status < 400 || strings.Contains(raw, "abi_") {
				t.Fatalf("foreign collection: %d %s", status, raw)
			}
		})
	}
	page := s.cookieBrowser(s.owner)
	c := page.do("POST", path+"/collect", nil)
	if c.status != 403 || strings.Contains(string(c.raw), "abi_") {
		t.Fatalf("browser collection: %d %s", c.status, c.raw)
	}
	for _, token := range []string{sibling, foreign} {
		status, raw = onboardingCall(t, s, "GET", path, "", token)
		if status != 404 || !strings.Contains(raw, "approval_not_found") {
			t.Fatalf("foreign metadata: %d %s", status, raw)
		}
	}
	status, raw = onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
	if status != 200 || !strings.Contains(raw, "abi_") {
		t.Fatalf("refusals consumed requesting outcome: %d %s", status, raw)
	}
}

func TestUnavailableApprovalInvitesNeverRevealAnOutcome(t *testing.T) {
	for _, state := range []string{"pending", "declined", "revoked", "redeemed", "expired", "removed"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			name, agent, _ := s.pair("starter")
			path := heldInvite(t, s, agent)
			if state == "declined" {
				status, raw := onboardingCall(t, s, "POST", path+"/decline", `{}`, s.owner)
				if status != 200 {
					t.Fatalf("decline: %d %s", status, raw)
				}
			}
			if state != "pending" && state != "declined" {
				status, raw := onboardingCall(t, s, "POST", path+"/allow", `{}`, s.owner)
				var out struct {
					Invite struct {
						ID     string `json:"id"`
						Invite string `json:"invite"`
					} `json:"invite"`
				}
				if err := json.Unmarshal([]byte(raw), &out); err != nil || status != 200 {
					t.Fatalf("allow: %d %s %v", status, raw, err)
				}
				switch state {
				case "revoked":
					c, err := s.client(s.owner).RevokeServerInviteWithResponse(t.Context(), out.Invite.ID, nil)
					mustStatus(t, c, err, 200)
				case "redeemed":
					mustStatus(t, s.connect(out.Invite.Invite, "redeemed"), nil, 201)
				case "expired":
					s.clock.Advance(25 * time.Hour)
				case "removed":
					c, err := s.client(s.owner).RemoveAgentWithResponse(t.Context(), name, "writer", nil)
					mustStatus(t, c, err, 200)
				}
			}
			status, raw := onboardingCall(t, s, "POST", path+"/collect", `{}`, agent)
			if strings.Contains(raw, "abi_") || strings.Contains(raw, `"collected":true`) {
				t.Fatalf("unavailable secret exposed: %d %s", status, raw)
			}
			if state != "removed" && status != 200 {
				t.Fatalf("unavailable metadata: %d %s", status, raw)
			}
		})
	}
}
