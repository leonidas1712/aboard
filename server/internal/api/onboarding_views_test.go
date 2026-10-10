package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestInvitePreviewDoesNotRedeemAndRefusesClosedInvitesUniformly(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	status, raw := onboardingCall(t, s, "POST", "/v1/invites", `{}`, s.owner)
	if status != 201 {
		t.Fatalf("issue: %d %s", status, raw)
	}
	var inv struct {
		Invite string `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &inv); err != nil {
		t.Fatal(err)
	}
	body := `{"invite":"` + inv.Invite + `"}`
	for range 2 {
		status, raw = onboardingCall(t, s, "POST", "/v1/invites/preview", body, "")
		if status != 200 || !strings.Contains(raw, `"inviter_handle":"alex"`) || strings.Contains(raw, inv.Invite) {
			t.Fatalf("preview: %d %s", status, raw)
		}
	}
	mustStatus(t, s.connect(inv.Invite, "newcomer"), nil, 201)
	status, used := onboardingCall(t, s, "POST", "/v1/invites/preview", body, "")
	if status != 404 {
		t.Fatalf("used: %d %s", status, used)
	}
	status, invalid := onboardingCall(t, s, "POST", "/v1/invites/preview", `{"invite":"abi_wrong"}`, "")
	if status != 404 || invalid != used {
		t.Fatalf("nonuniform invalid: %d %s versus %s", status, invalid, used)
	}
	s.clock.Advance(time.Minute)
	status, raw = onboardingCall(t, s, "POST", "/v1/invites", `{"ttl_seconds":60}`, s.owner)
	if status != 201 {
		t.Fatalf("issue short: %d %s", status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &inv); err != nil {
		t.Fatal(err)
	}
	s.clock.Advance(time.Minute)
	status, expired := onboardingCall(t, s, "POST", "/v1/invites/preview", `{"invite":"`+inv.Invite+`"}`, "")
	if status != 404 || expired != used {
		t.Fatalf("nonuniform expiry: %d %s", status, expired)
	}
}

func TestApprovalExpiresAndItsDecisionLeavesOnlyTheInbox(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agent, _ := s.pair("starter")
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{}}`, agent)
	if status != 202 {
		t.Fatalf("request: %d %s", status, raw)
	}
	var held struct {
		Approval struct {
			ID        string `json:"id"`
			ExpiresAt string `json:"expires_at"`
		} `json:"approval"`
	}
	if err := json.Unmarshal([]byte(raw), &held); err != nil {
		t.Fatal(err)
	}
	if held.Approval.ExpiresAt != "2026-10-02T16:00:00Z" {
		t.Fatalf("missing 24h expiry: %s", raw)
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals?state=pending", "", s.owner)
	if status != 200 || !strings.Contains(raw, held.Approval.ID) || !strings.Contains(raw, `"person_handle":"alex"`) {
		t.Fatalf("pending display: %d %s", status, raw)
	}
	s.clock.Advance(24 * time.Hour)
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals?state=decided", "", s.owner)
	if status != 200 || !strings.Contains(raw, `"state":"expired"`) {
		t.Fatalf("expired view: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/allow", `{}`, s.owner)
	if status != 409 || !strings.Contains(raw, "approval_closed") {
		t.Fatalf("expired execution: %d %s", status, raw)
	}
	s.clock.Advance(7 * 24 * time.Hour)
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals?state=decided", "", s.owner)
	if status != 200 || strings.Contains(raw, held.Approval.ID) {
		t.Fatalf("old decision still in inbox: %d %s", status, raw)
	}
}

func TestInvitePreviewShowsOnlyTheValidBundleAndSharesJoinLimit(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := t.Context()
	name := s.privateBoard(s.owner)
	_, token := s.joinAs(s.owner, name, "member", "codex")
	seat, err := s.client(token).GetInboxWithResponse(ctx, nil)
	mustStatus(t, seat, err, 200)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	boards := []string{b.JSON200.Id}
	issued, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &boards, Pairing: &api.InvitePairing{InitiatingAgentId: *seat.JSON200.MemberId, Work: "Review the brief together."}})
	mustStatus(t, issued, err, 201)
	preview, err := s.client("").PreviewServerInviteWithResponse(ctx, api.PreviewServerInviteJSONRequestBody{Invite: issued.JSON201.Invite})
	mustStatus(t, preview, err, 200)
	if len(preview.JSON200.Boards) != 1 || preview.JSON200.Boards[0].Name != name || preview.JSON200.Work == nil || *preview.JSON200.Work != "Review the brief together." {
		t.Fatalf("bundle: %s", preview.Body)
	}
	if preview.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("preview can be cached")
	}
	pairing, err := s.client(s.owner).ListPairingRequestsWithResponse(ctx)
	mustStatus(t, pairing, err, 200)
	if len(pairing.JSON200.Requests) != 1 || pairing.JSON200.Requests[0].Display == nil || pairing.JSON200.Requests[0].Display.AgentName == nil || *pairing.JSON200.Requests[0].Display.AgentName != "codex" {
		t.Fatalf("pairing labels: %s", pairing.Body)
	}
	revoked, err := s.client(s.owner).RevokeServerInviteWithResponse(ctx, issued.JSON201.Id, nil)
	mustStatus(t, revoked, err, 200)
	refused, err := s.client("").PreviewServerInviteWithResponse(ctx, api.PreviewServerInviteJSONRequestBody{Invite: issued.JSON201.Invite})
	wantCode(t, refused, 404, "invite_invalid")
	if err != nil {
		t.Fatal(err)
	}
	// The earlier agent join and both previews spent three of the five attempts.
	for range 2 {
		refused, err = s.client("").PreviewServerInviteWithResponse(ctx, api.PreviewServerInviteJSONRequestBody{Invite: "abi_wrong"})
		mustStatus(t, refused, err, 404)
	}
	limited, err := s.client("").PreviewServerInviteWithResponse(ctx, api.PreviewServerInviteJSONRequestBody{Invite: "abi_wrong"})
	mustStatus(t, limited, err, 429)
}

func TestOnboardingLabelsDisappearWhenAnAdminLosesPrivateBoardAccess(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := t.Context()
	name := s.privateBoard(s.owner)
	_, token := s.joinAs(s.owner, name, "member", "codex")
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	maya := s.addHuman("maya")
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "maya"})
	mustStatus(t, added, err, 201)
	owner, err := s.client(s.owner).AddOwnerWithResponse(ctx, name, nil, api.AddPersonRequest{Handle: "maya"})
	mustStatus(t, owner, err, 200)
	status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", `{"kind":"invite_people","invite":{"boards":["`+b.JSON200.Id+`"]}}`, token)
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
	status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/allow", `{}`, s.owner)
	if status != 200 || !strings.Contains(raw, `"kind":"invited"`) {
		t.Fatalf("approval execution kind: %d %s", status, raw)
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/me/invite-notices", "", s.browserToken(s.owner))
	if status != 200 || !strings.Contains(raw, `"agent_name":"codex"`) || !strings.Contains(raw, name) {
		t.Fatalf("visible notice: %d %s", status, raw)
	}
	removed, err := s.client(maya).RemovePersonWithResponse(ctx, name, "alex", nil)
	mustStatus(t, removed, err, 200)
	for _, path := range []string{"/v1/me/invite-notices", "/v1/me/approvals", "/v1/pairing-requests"} {
		status, raw = onboardingCall(t, s, "GET", path, "", s.owner)
		if status != 200 || strings.Contains(raw, name) || strings.Contains(raw, `"agent_name"`) {
			t.Fatalf("hidden metadata in %s: %d %s", path, status, raw)
		}
	}
}

func TestExpiredApprovalCannotExecuteWithoutAListRead(t *testing.T) {
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
	s.clock.Advance(24 * time.Hour)
	for _, action := range []string{"allow", "decline"} {
		status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/"+action, `{}`, s.owner)
		if status != 409 || !strings.Contains(raw, "approval_closed") {
			t.Fatalf("expired %s: %d %s", action, status, raw)
		}
	}
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals?state=decided", "", s.owner)
	if status != 200 || !strings.Contains(raw, `"state":"expired"`) {
		t.Fatalf("decision: %d %s", status, raw)
	}
	s.clock.Advance(7 * 24 * time.Hour)
	status, raw = onboardingCall(t, s, "GET", "/v1/me/approvals", "", s.owner)
	if status != 200 || strings.Contains(raw, held.Approval.ID) {
		t.Fatalf("old decision: %d %s", status, raw)
	}
	if err := s.st.Read(t.Context(), func(tx board.ReadTx) error {
		a, err := tx.Approval(held.Approval.ID)
		if err != nil {
			return err
		}
		if a.State != "expired" || a.DecidedAt == nil {
			t.Fatalf("record removed: %+v", a)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInvitePreviewRechecksIssuingSeatAndAdminAuthority(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, agent, _ := s.pair("starter")
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
	status, raw = onboardingCall(t, s, "POST", "/v1/me/approvals/"+held.Approval.ID+"/allow", `{}`, s.owner)
	if status != 200 {
		t.Fatalf("allow: %d %s", status, raw)
	}
	var issued struct {
		Invite struct {
			Secret string `json:"invite"`
		} `json:"invite"`
	}
	if err := json.Unmarshal([]byte(raw), &issued); err != nil {
		t.Fatal(err)
	}
	req := api.PreviewServerInviteJSONRequestBody{Invite: issued.Invite.Secret}
	preview, err := s.client("").PreviewServerInviteWithResponse(t.Context(), req)
	mustStatus(t, preview, err, 200)
	removed, err := s.client(s.owner).RemoveAgentWithResponse(t.Context(), name, "writer", nil)
	mustStatus(t, removed, err, 200)
	invalid, err := s.client("").PreviewServerInviteWithResponse(t.Context(), req)
	wantCode(t, invalid, 404, "invite_invalid")
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary invitations also end if their issuing person loses admin authority.
	maya := s.addHuman("maya")
	promoted, err := s.client(s.owner).SetServerRoleWithResponse(t.Context(), "maya", nil, api.ServerRoleChange{ServerRole: "admin"})
	mustStatus(t, promoted, err, 200)
	inv := s.invite(maya, 0)
	mustStatus(t, inv, nil, 201)
	demoted, err := s.client(s.owner).SetServerRoleWithResponse(t.Context(), "maya", nil, api.ServerRoleChange{ServerRole: "member"})
	mustStatus(t, demoted, err, 200)
	invalid, err = s.client("").PreviewServerInviteWithResponse(t.Context(), api.PreviewServerInviteJSONRequestBody{Invite: inv.JSON201.Invite})
	wantCode(t, invalid, 404, "invite_invalid")
	if err != nil {
		t.Fatal(err)
	}
}
