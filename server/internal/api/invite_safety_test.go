package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgentInviteSafetyForAllowanceAndApproval(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	type inviteResult struct {
		Invite struct {
			ID      string    `json:"id"`
			Secret  string    `json:"invite"`
			Expires time.Time `json:"expires_at"`
		} `json:"invite"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	issued := []inviteResult{}
	for _, mode := range []string{"allowance", "approval"} {
		categories := `{"categories":[]}`
		if mode == "allowance" {
			categories = `{"categories":["invite-people"]}`
		}
		status, raw := onboardingCall(t, s, "PUT", "/v1/me/allowance", categories, s.owner)
		if status != 200 {
			t.Fatalf("set: %d %s", status, raw)
		}
		if mode == "allowance" && !strings.Contains(raw, "Agents allowed to invite people can let outsiders read every open board.") {
			t.Fatalf("missing warning: %s", raw)
		}
		status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
		var result inviteResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		if mode == "approval" {
			if status != 202 {
				t.Fatalf("held: %d %s", status, raw)
			}
			status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+result.Approval.ID+"/allow", `{}`, s.owner)
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
		}
		if status != 201 && status != 200 {
			t.Fatalf("issue: %d %s", status, raw)
		}
		if result.Invite.Expires.Sub(s.clock.Now()) != 24*time.Hour {
			t.Fatalf("%s TTL: %s", mode, raw)
		}
		issued = append(issued, result)
	}
	// A quickly redeemed invitation must still tell its person what happened.
	mustStatus(t, s.connect(issued[0].Invite.Secret, "new-person"), nil, 201)
	status, raw := onboardingCall(t, s, "GET", "/v1/me/invite-notices", "", s.owner)
	if status != 200 {
		t.Fatalf("notices: %d %s", status, raw)
	}
	var notices struct {
		Notices []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Message string `json:"message"`
			Next    struct {
				Command string `json:"command"`
			} `json:"next"`
		} `json:"notices"`
	}
	if err := json.Unmarshal([]byte(raw), &notices); err != nil {
		t.Fatal(err)
	}
	if len(notices.Notices) != 2 {
		t.Fatalf("notices: %s", raw)
	}
	for _, n := range notices.Notices {
		if n.Message != "Your agent invited someone" || !strings.Contains(n.Next.Command, n.ID) || !strings.Contains(n.Next.Command, "--server") {
			t.Fatalf("notice handoff: %s", raw)
		}
	}
	if strings.Contains(raw, "abi_") || strings.Contains(raw, "parent_key_id") || strings.Contains(raw, "boards") {
		t.Fatalf("notice leaked invite material: %s", raw)
	}
	for _, token := range []string{agent, s.addHuman("other")} {
		status, raw = onboardingCall(t, s, "GET", "/v1/me/invite-notices", "", token)
		if token == agent {
			if status != 403 || !strings.Contains(raw, "human_token_required") || !strings.Contains(raw, "command") {
				t.Fatalf("agent notices: %d %s", status, raw)
			}
		} else if status != 200 || !strings.Contains(raw, `"notices":[]`) {
			t.Fatalf("foreign notices: %d %s", status, raw)
		}
	}
	page := s.cookieBrowser(s.owner)
	browser := s.send("GET", "/v1/me/invite-notices", nil, func(r *http.Request) { r.AddCookie(page.cookie); s.fromPage(r) })
	if browser.status != 200 || !strings.Contains(string(browser.raw), issued[0].Invite.ID) {
		t.Fatalf("browser notice: %d %s", browser.status, browser.raw)
	}
	management := s.send("GET", "/v1/invites", nil, func(r *http.Request) { r.AddCookie(page.cookie); s.fromPage(r) })
	if management.status != 403 {
		t.Fatalf("browser invite management expanded: %d %s", management.status, management.raw)
	}
}

func TestInviteSafetyPreservesPersonDefaultAndExplicitAgentLifetime(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "POST", "/v1/invites", `{}`, s.owner)
	var person struct {
		Expires time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(raw), &person); err != nil {
		t.Fatal(err)
	}
	if status != 201 || person.Expires.Sub(s.clock.Now()) != 7*24*time.Hour {
		t.Fatalf("person default: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":["invite-people"]}`, s.owner)
	if status != 200 {
		t.Fatalf("allowance: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"ttl_seconds":120}}`, agent)
	var result struct {
		Invite struct {
			Expires time.Time `json:"expires_at"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if status != 201 || result.Invite.Expires.Sub(s.clock.Now()) != 2*time.Minute {
		t.Fatalf("explicit agent lifetime: %d %s", status, raw)
	}
}
