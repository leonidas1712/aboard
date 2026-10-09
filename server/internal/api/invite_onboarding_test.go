package api_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func onboardingToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "abh_" + base64.RawURLEncoding.EncodeToString(b)
}

func TestClientConnectRecoversOnlyWithOriginalWorkingKey(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	inv := s.invite(s.owner, 0)
	mustStatus(t, inv, nil, 201)
	token := onboardingToken(t)
	req := api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &token}
	c, err := s.client("").ConnectWithResponse(ctx, nil, req)
	mustStatus(t, c, err, 200)
	if strings.Contains(string(c.Body), token) || strings.Contains(string(c.Body), `"token"`) {
		t.Fatalf("secret in response: %s", c.Body)
	}
	s.restart()
	receipt, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, receipt, err, 200)
	if receipt.JSON200.InviteId != inv.JSON201.Id || receipt.JSON200.KeyId != c.JSON200.Key.Id {
		t.Fatalf("wrong durable receipt: %s", receipt.Body)
	}
	repeat, err := s.client("").ConnectWithResponse(ctx, nil, req)
	mustStatus(t, repeat, err, 404)
	absent, err := s.client(s.owner).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, absent, err, 404)
	revoked, err := s.client(token).RevokeKeyWithResponse(ctx, c.JSON200.Key.Id, nil)
	mustStatus(t, revoked, err, 200)
	gone, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, gone, err, 401)
	legacy := s.connect(s.invite(s.owner, 0).JSON201.Invite, "sam")
	mustStatus(t, legacy, nil, 201)
	if legacy.JSON201.Key.Token == "" {
		t.Fatal("legacy token missing")
	}
}

func TestBundledInviteAdmitsAtomicallyAndRecoveryDoesNotRestoreMembership(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	tpl := "general"
	vis := api.BoardVisibility("private")
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Template: &tpl, Visibility: &vis})
	mustStatus(t, b, err, 201)
	boards := []string{b.JSON201.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &boards})
	mustStatus(t, inv, err, 201)
	token := onboardingToken(t)
	c, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, c, err, 200)
	if len(c.JSON200.Onboarding.Boards) != 1 {
		t.Fatalf("missing membership: %s", c.Body)
	}
	removed, err := s.client(s.owner).RemovePersonWithResponse(ctx, b.JSON201.Name, "maya", nil)
	mustStatus(t, removed, err, 200)
	receipt, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, receipt, err, 200)
	if len(receipt.JSON200.Boards) != 0 {
		t.Fatalf("hidden membership returned: %s", receipt.Body)
	}
}

func TestOwnInviteMetadataAndRevocationWithoutAdminRole(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	other := s.addHuman("maya")
	promoted, err := s.client(s.owner).SetServerRoleWithResponse(ctx, "maya", nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRole("admin")})
	mustStatus(t, promoted, err, 200)
	inv := s.invite(other, 0)
	mustStatus(t, inv, nil, 201)
	demoted, err := s.client(s.owner).SetServerRoleWithResponse(ctx, "maya", nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRole("member")})
	mustStatus(t, demoted, err, 200)
	listed, err := s.client(other).ListServerInvitesWithResponse(ctx)
	mustStatus(t, listed, err, 200)
	if len(listed.JSON200.Invites) != 1 || strings.Contains(string(listed.Body), inv.JSON201.Invite) {
		t.Fatalf("metadata: %s", listed.Body)
	}
	foreign, err := s.client(s.owner).RevokeServerInviteWithResponse(ctx, inv.JSON201.Id, nil)
	mustStatus(t, foreign, err, 404)
	revoked, err := s.client(other).RevokeServerInviteWithResponse(ctx, inv.JSON201.Id, nil)
	mustStatus(t, revoked, err, 200)
	if !revoked.JSON200.Changed {
		t.Fatal("initial revoke unchanged")
	}
	repeat, err := s.client(other).RevokeServerInviteWithResponse(ctx, inv.JSON201.Id, nil)
	mustStatus(t, repeat, err, 200)
	if repeat.JSON200.Changed {
		t.Fatal("repeat revoke changed")
	}
	wantCode(t, s.connect(inv.JSON201.Invite, "sam"), 404, "invite_invalid")
}

func TestBundledInviteRechecksIssuerAndDoesNotPartiallyRedeem(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName := s.privateBoard(s.owner)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, b, err, 200)
	boards := []string{b.JSON200.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &boards})
	mustStatus(t, inv, err, 201)
	s.addHuman("sam")
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: "sam"})
	mustStatus(t, added, err, 201)
	owner, err := s.client(s.owner).AddOwnerWithResponse(ctx, boardName, nil, api.AddPersonRequest{Handle: "sam"})
	mustStatus(t, owner, err, 200)
	left, err := s.client(s.owner).LeaveBoardWithResponse(ctx, boardName, nil)
	mustStatus(t, left, err, 200)
	token := onboardingToken(t)
	refused, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, refused, err, 404)
	unknown, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, unknown, err, 401)
	replacement := s.connect(s.invite(s.owner, 0).JSON201.Invite, "maya")
	mustStatus(t, replacement, nil, 201)
}

func TestClientConnectRejectsNoncanonicalOrReusedTokenBeforeSpendingInvite(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	inv := s.invite(s.owner, 0)
	token := onboardingToken(t)
	for _, bad := range []string{token + "=", "abh_" + strings.Repeat("A", 42) + "B", "abh_" + strings.Repeat("A", 42), "aba_" + token[4:]} {
		refused, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &bad})
		mustStatus(t, refused, err, 400)
	}
	fresh, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, fresh, err, 200)
	second := s.invite(s.owner, 0)
	reused, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: second.JSON201.Invite, Handle: "sam", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, reused, err, 400)
	other := onboardingToken(t)
	retry, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: second.JSON201.Invite, Handle: "sam", KeyName: "laptop", ClientToken: &other})
	mustStatus(t, retry, err, 200)
}

func TestIssuedAgentInvitesEndWithoutRemovingRedeemedPerson(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, agent, _ := s.pair("starter")
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, b, err, 200)
	me, err := s.client(agent).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	status, raw := onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":["invite-people","add-people"]}`, s.owner)
	if status != 200 {
		t.Fatalf("allowance: %d %s", status, raw)
	}
	issue := func() string {
		payload := `{"kind":"invite_people","invite":{"boards":["` + b.JSON200.Id + `"]}}`
		status, raw := onboardingCall(t, s, "POST", "/v1/me/admin-requests", payload, agent)
		if status != 201 {
			t.Fatalf("agent bundle: %d %s", status, raw)
		}
		var out struct {
			Invite struct {
				Invite string `json:"invite"`
			} `json:"invite"`
		}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out.Invite.Invite
	}
	first, second := issue(), issue()
	status, raw = onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":[]}`, s.owner)
	if status != 200 {
		t.Fatalf("disable: %d %s", status, raw)
	}
	token := onboardingToken(t)
	connected, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: first, Handle: "maya", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, connected, err, 200)
	listed, err := s.client(s.owner).ListServerInvitesWithResponse(ctx)
	mustStatus(t, listed, err, 200)
	if len(listed.JSON200.Invites) != 2 || listed.JSON200.Invites[0].IssuingAgentId == nil || *listed.JSON200.Invites[0].IssuingAgentId != me.JSON200.Id {
		t.Fatalf("issuer provenance: %s", listed.Body)
	}
	removed, err := s.client(s.owner).RemoveAgentWithResponse(ctx, boardName, me.JSON200.Id, nil)
	mustStatus(t, removed, err, 200)
	wantCode(t, s.connect(second, "sam"), 404, "invite_invalid")
	receipt, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, receipt, err, 200)
	if len(receipt.JSON200.Boards) != 1 {
		t.Fatalf("redeemed membership cascaded: %s", receipt.Body)
	}
	visible, err := s.client(token).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, visible, err, 200)
}

func TestInvitePairingAndReceiptCommitTogether(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	name, agent, _ := s.pair("starter")
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	me, err := s.client(agent).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	boards := []string{b.JSON200.Id}
	inv, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &boards, Pairing: &api.InvitePairing{InitiatingAgentId: me.JSON200.Id, Work: "Review the change"}})
	mustStatus(t, inv, err, 201)
	if inv.JSON201.PairingRequestId == nil {
		t.Fatal("pairing absent")
	}
	endpointToken := "abp_" + strings.Repeat("a", 43)
	minted, err := s.client(s.owner).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{
		RequestId: *inv.JSON201.PairingRequestId, Side: "initiator", AgentId: me.JSON200.Id,
		ClientToken: &endpointToken, SessionBinding: "sha256:" + strings.Repeat("a", 64), Generation: 1,
	})
	mustStatus(t, minted, err, 200)
	waiting, err := s.client(s.owner).GetPairingRequestWithResponse(ctx, *inv.JSON201.PairingRequestId)
	mustStatus(t, waiting, err, 200)
	if waiting.JSON200.State != api.PairingStateAwaitingAccount || waiting.JSON200.Initiator == nil {
		t.Fatal("selecting the inviting session lost the pending account state or endpoint")
	}
	token := onboardingToken(t)
	c, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: inv.JSON201.Invite, Handle: "maya", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, c, err, 200)
	if c.JSON200.Onboarding.PairingRequestId == nil || *c.JSON200.Onboarding.PairingRequestId != *inv.JSON201.PairingRequestId {
		t.Fatalf("pairing receipt: %s", c.Body)
	}
	s.restart()
	receipt, err := s.client(token).GetOnboardingReceiptWithResponse(ctx)
	mustStatus(t, receipt, err, 200)
	if receipt.JSON200.PairingRequestId == nil {
		t.Fatal("pairing receipt not durable")
	}
	list, err := s.client(token).ListPairingRequestsWithResponse(ctx)
	mustStatus(t, list, err, 200)
	if !strings.Contains(string(list.Body), *inv.JSON201.PairingRequestId) || !strings.Contains(string(list.Body), "awaiting_session") {
		t.Fatalf("pairing recipient: %s", list.Body)
	}
}
