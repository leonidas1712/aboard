package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAgentInviteWaitsForItsPersonAndExecutesOnlyOnce(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "GET", "/v1/me/allowance", "", s.owner)
	if status != http.StatusOK {
		t.Fatalf("default allowance: %d %s", status, raw)
	}
	var allowance struct {
		Categories []string `json:"categories"`
	}
	if err := json.Unmarshal([]byte(raw), &allowance); err != nil || len(allowance.Categories) != 0 {
		t.Fatalf("allowance must start off: %v %s", err, raw)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	if status != http.StatusAccepted {
		t.Fatalf("agent invite not held: %d %s", status, raw)
	}
	var held struct {
		State    string `json:"state"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
		Next struct {
			Command string `json:"command"`
		} `json:"next"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil || held.State != "pending" || held.Approval.ID == "" || !strings.Contains(held.Next.Command, held.Approval.ID) || strings.Contains(raw, "abi_") {
		t.Fatalf("held result missing safe handover: %v %s", err, raw)
	}
	other := s.addHuman("other")
	path := "/v1/me/approvals/" + held.Approval.ID + "/allow"
	status, raw = onboardingCall(t, s, "POST", path, `{}`, other)
	if status != http.StatusNotFound {
		t.Fatalf("another person sees or executes approval: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "POST", path, `{}`, agent)
	if status != http.StatusForbidden || !strings.Contains(raw, `"human_token_required"`) || !strings.Contains(raw, `"command"`) {
		t.Fatalf("agent approved itself or lost handover: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "POST", path, `{}`, s.owner)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("person approval failed: %d %s", status, raw)
	}
	var executed struct {
		Invite struct {
			Invite string `json:"invite"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &executed); err != nil || executed.Invite.Invite == "" {
		t.Fatalf("execution did not return new invite: %v %s", err, raw)
	}
	mustStatus(t, s.connect(executed.Invite.Invite, "invited"), nil, 201)
	status, raw = onboardingCall(t, s, "POST", path, `{}`, s.owner)
	if status != http.StatusOK || strings.Contains(raw, "abi_") || !strings.Contains(raw, `"execution"`) {
		t.Fatalf("repeat reissued secret or lost execution record: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals", "", s.owner)
	if status != http.StatusOK || strings.Contains(raw, "abi_") || !strings.Contains(raw, held.Approval.ID) {
		t.Fatalf("approval inventory leaked secret or lost record: %d %s", status, raw)
	}
}

func TestAdministrativeRequestReplayIsNonsecretAndRespectsAllowanceRevocation(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":["invite-people"]}`, s.owner)
	if status != 200 {
		t.Fatalf("enable allowance: %d %s", status, raw)
	}
	request := func(body string) call {
		var payload map[string]any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		return s.send("POST", "/v1/me/admin-requests", payload, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+agent)
			r.Header.Set("Idempotency-Key", "one-exact-invite")
		})
	}
	first := request(`{"kind":"invite_people","invite":{}}`)
	if first.status != 201 || !strings.Contains(string(first.raw), "abi_") {
		t.Fatalf("automatic invite: %d %s", first.status, first.raw)
	}
	repeat := request(`{"kind":"invite_people","invite":{}}`)
	if repeat.status != 200 || strings.Contains(string(repeat.raw), "abi_") {
		t.Fatalf("secret replay: %d %s", repeat.status, repeat.raw)
	}
	changed := request(`{"kind":"invite_people","invite":{"ttl_seconds":120}}`)
	if changed.status != 409 || changed.code() != "idempotency_conflict" {
		t.Fatalf("changed payload: %d %s", changed.status, changed.raw)
	}
	status, raw = onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":[]}`, s.owner)
	if status != 200 {
		t.Fatalf("disable allowance: %d %s", status, raw)
	}
	denied := request(`{"kind":"invite_people","invite":{}}`)
	if denied.status != 403 {
		t.Fatalf("cached answer bypassed revoked allowance: %d %s", denied.status, denied.raw)
	}
}

func TestOnboardingBrowserDecisionsNeedOriginAndCSRF(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	if status != 202 {
		t.Fatalf("held: %d %s", status, raw)
	}
	var held struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil {
		t.Fatal(err)
	}
	page := s.cookieBrowser(s.owner)
	for _, tc := range []struct {
		method, path string
		body         map[string]any
	}{
		{"PUT", "/v1/me/allowance", map[string]any{"categories": []string{}}},
		{"POST", "/v1/me/approvals/" + held.Approval.ID + "/allow", map[string]any{}},
		{"POST", "/v1/me/approvals/" + held.Approval.ID + "/decline", nil},
	} {
		forged := s.send(tc.method, tc.path, tc.body, func(r *http.Request) { r.AddCookie(page.cookie); s.fromPage(r) })
		if forged.status != 403 || forged.code() != "csrf_token_invalid" {
			t.Fatalf("missing CSRF: %d %s", forged.status, forged.raw)
		}
	}
	declined := page.do("POST", "/v1/me/approvals/"+held.Approval.ID+"/decline", nil)
	if declined.status != 200 || declined.body["state"] != "declined" {
		t.Fatalf("browser decline: %d %s", declined.status, declined.raw)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/allow", `{}`, s.owner)
	if status != 409 || !strings.Contains(raw, "approval_closed") {
		t.Fatalf("declined action executed: %d %s", status, raw)
	}
}

func TestAgentExactPersonLookupOmitsDirectoryAndGuestExistence(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, agent, _ := s.pair("starter")
	s.addHuman("maya")
	guest := s.guestCode(s.owner, name, "guest")
	mustStatus(t, guest, nil, 201)
	mustStatus(t, s.guestJoin(*guest.JSON201.Code), nil, 201)
	status, raw := onboardingCall(t, s, "GET", "/v1/people?handle=maya", "", agent)
	if status != 200 {
		t.Fatalf("lookup: %d %s", status, raw)
	}
	var found map[string]any
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		t.Fatal(err)
	}
	identity, ok := found["id"].(string)
	if !ok || len(found) != 3 || found["handle"] != "maya" || !strings.HasPrefix(identity, "hum_") {
		t.Fatalf("lookup expanded metadata: %s", raw)
	}
	_, missing := onboardingCall(t, s, "GET", "/v1/people?handle=missing", "", agent)
	for _, handle := range []string{"guest", "may"} {
		status, raw = onboardingCall(t, s, "GET", "/v1/people?handle="+handle, "", agent)
		if status != 404 || raw != missing {
			t.Fatalf("guest/prefix probe differs: %d %s / %s", status, raw, missing)
		}
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/people", "", agent)
	if status != 403 {
		t.Fatalf("agent got directory: %d %s", status, raw)
	}
}
