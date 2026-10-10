package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBrowserRevokesOnlyOwnInviteWithCSRF(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	status, raw := onboardingCall(t, s, "POST", "/v1/invites", `{}`, s.owner)
	if status != 201 {
		t.Fatalf("invite: %d %s", status, raw)
	}
	var issued struct {
		ID     string `json:"id"`
		Invite string `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &issued); err != nil {
		t.Fatal(err)
	}
	path := "/v1/invites/" + issued.ID
	page := s.cookieBrowser(s.owner)
	forged := s.send(http.MethodDelete, path, nil, func(r *http.Request) { r.AddCookie(page.cookie); s.fromPage(r) })
	if forged.status != 403 || forged.code() != "csrf_token_invalid" {
		t.Fatalf("CSRF: %d %s", forged.status, forged.raw)
	}
	foreign := s.cookieBrowser(s.addHuman("maya")).do(http.MethodDelete, path, nil)
	if foreign.status != 404 {
		t.Fatalf("foreign: %d %s", foreign.status, foreign.raw)
	}
	good := page.do(http.MethodDelete, path, nil)
	if good.status != 200 {
		t.Fatalf("browser revoke: %d %s", good.status, good.raw)
	}
	repeated := page.do(http.MethodDelete, path, nil)
	if repeated.status != 200 {
		t.Fatalf("repeat: %d %s", repeated.status, repeated.raw)
	}
	previewStatus, _ := onboardingCall(t, s, "POST", "/v1/invites/preview", `{"invite":"`+issued.Invite+`"}`, "")
	if previewStatus != 404 {
		t.Fatalf("revoked preview: %d", previewStatus)
	}
}

func TestApprovalKeepsOnceOrAlwaysAfterAllowanceChanges(t *testing.T) {
	for _, mode := range []string{"once", "always"} {
		t.Run(mode, func(t *testing.T) {
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
			status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/allow", `{"always":`+map[string]string{"once": "false", "always": "true"}[mode]+`}`, s.owner)
			if status != 200 || !strings.Contains(raw, `"decision":"`+mode+`"`) {
				t.Fatalf("decision: %d %s", status, raw)
			}
			status, raw = onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":[]}`, s.owner)
			if status != 200 {
				t.Fatalf("allowance: %d %s", status, raw)
			}
			status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals?state=decided", "", s.owner)
			if status != 200 || !strings.Contains(raw, `"decision":"`+mode+`"`) {
				t.Fatalf("recorded mode: %d %s", status, raw)
			}
		})
	}
}

func TestRevokeKeyApprovalNamesItsTarget(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	key := s.createKey(s.owner, "work-laptop", 0)
	mustStatus(t, key, nil, 201)
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"revoke_key","key_id":"`+key.JSON201.Id+`"}`, agent)
	if status != 202 || !strings.Contains(raw, `"key_name":"work-laptop"`) {
		t.Fatalf("key display: %d %s", status, raw)
	}
}

func TestAdminAgentRevokeKeyApprovalNamesForeignTarget(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	maya := s.addHuman("maya")
	key := s.createKey(maya, "maya-laptop", 0)
	mustStatus(t, key, nil, 201)
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"revoke_key","key_id":"`+key.JSON201.Id+`"}`, agent)
	if status != 202 || !strings.Contains(raw, `"key_name":"maya-laptop"`) {
		t.Fatalf("admin target display: %d %s", status, raw)
	}
}
