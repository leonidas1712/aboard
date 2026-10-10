package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestOrdinaryAgentBoardInviteKeepsItsExactInvitingSeat(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, agent, _ := s.pair("starter")
	first, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, first, err, 200)
	otherName := s.privateBoard(s.owner)
	other, err := s.client(s.owner).GetBoardWithResponse(ctx, otherName)
	mustStatus(t, other, err, 200)
	me, err := s.client(agent).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	status, raw := onboardingCall(t, s, "PUT", "/v1/me/allowance", `{"categories":["invite-people","add-people"]}`, s.owner)
	if status != 200 {
		t.Fatalf("allowance: %d %s", status, raw)
	}
	// The issuing seat's board is deliberately not the first bundled board.
	body := fmt.Sprintf(`{"kind":"invite_people","invite":{"boards":[%q,%q]}}`, other.JSON200.Id, first.JSON200.Id)
	status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", body, agent)
	var issued struct {
		Invite api.ServerInvite `json:"invite"`
	}
	if status != 201 || json.Unmarshal([]byte(raw), &issued) != nil || issued.Invite.PairingRequestId == nil {
		t.Fatalf("ordinary bundled invite lacks hidden verification: %d %s", status, raw)
	}
	r, err := s.client(s.owner).GetPairingRequestWithResponse(ctx, *issued.Invite.PairingRequestId)
	mustStatus(t, r, err, 200)
	if r.JSON200.BoardId != first.JSON200.Id || r.JSON200.InitiatingAgentId != me.JSON200.Id || r.JSON200.State != api.PairingStateAwaitingAccount || r.JSON200.Initiator != nil || r.JSON200.Recipient != nil {
		t.Fatalf("hidden check guessed or selected an endpoint: %s", r.Body)
	}
	withoutOrigin := fmt.Sprintf(`{"kind":"invite_people","invite":{"boards":[%q]}}`, other.JSON200.Id)
	status, raw = onboardingCall(t, s, "POST", "/v1/me/admin-requests", withoutOrigin, agent)
	var unverified struct {
		Invite api.ServerInvite `json:"invite"`
	}
	if status != 201 || json.Unmarshal([]byte(raw), &unverified) != nil || unverified.Invite.PairingRequestId != nil {
		t.Fatalf("invite without the issuing board guessed a hidden endpoint: %d %s", status, raw)
	}
	personInvite, err := s.client(s.owner).CreateServerInviteWithResponse(ctx, nil, api.CreateInviteRequest{Boards: &[]string{first.JSON200.Id}})
	mustStatus(t, personInvite, err, 201)
	if personInvite.JSON201.PairingRequestId != nil {
		t.Fatal("person invitation guessed an agent from activity")
	}
	token := onboardingToken(t)
	connected, err := s.client("").ConnectWithResponse(ctx, nil, api.ConnectRequest{Invite: issued.Invite.Invite, Handle: "newcomer", KeyName: "laptop", ClientToken: &token})
	mustStatus(t, connected, err, 200)
	if len(connected.JSON200.Onboarding.Boards) != 2 || connected.JSON200.Onboarding.PairingRequestId == nil || *connected.JSON200.Onboarding.PairingRequestId != r.JSON200.Id {
		t.Fatal("redemption lost memberships or the original hidden check")
	}
}

func TestDelegatedSetupJoinUsesTheImmutableBoardIdentity(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	person := s.addHuman("newcomer")
	name := s.privateBoard(person)
	b := s.call("GET", "/v1/boards/"+name, person, nil, "")
	if b.status != 200 {
		t.Fatalf("board: %d %s", b.status, b.raw)
	}
	d := s.delegation(person, "laptop")
	first := s.joinSession(d.str("token"), b.str("id"), "codex:setup-session", nil)
	if first.status != 201 || first.str("board", "id") != b.str("id") {
		t.Fatalf("receipt-id join: %d %s", first.status, first.raw)
	}
	repeat := s.joinSession(d.str("token"), b.str("id"), "codex:setup-session", nil)
	if repeat.status != 200 || repeat.body["reused"] != true || repeat.str("agent", "id") != first.str("agent", "id") {
		t.Fatalf("same-session repeat: %d %s", repeat.status, repeat.raw)
	}
	outsider := s.addHuman("outsider")
	foreign := s.delegation(outsider, "laptop")
	s.want(s.joinSession(foreign.str("token"), b.str("id"), "codex:outside-session", nil), 404, "board_not_found")
}

func TestDelegatedSetupJoinDoesNotRestoreRemovedOpenBoardMembership(t *testing.T) {
	t.Parallel()
	s := newJoinServer(t)
	ctx := context.Background()
	visibility := api.BoardVisibility("open")
	b, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Visibility: &visibility})
	mustStatus(t, b, err, 201)
	person := s.addHuman("newcomer")
	added, err := s.client(s.owner).AddPersonWithResponse(ctx, b.JSON201.Name, nil, api.AddPersonRequest{Handle: "newcomer"})
	mustStatus(t, added, err, 201)
	d := s.delegation(person, "laptop")
	s.want(s.joinSession(d.str("token"), b.JSON201.Id, "codex:setup-session", nil), 201, "")
	removed, err := s.client(s.owner).RemovePersonWithResponse(ctx, b.JSON201.Name, "newcomer", nil)
	mustStatus(t, removed, err, 200)
	for _, session := range []string{"codex:setup-session", "codex:another-session"} {
		s.want(s.joinSession(d.str("token"), b.JSON201.Id, session, nil), 404, "board_not_found")
	}
	view, err := s.client(person).GetBoardWithResponse(ctx, b.JSON201.Name)
	mustStatus(t, view, err, 200)
	if view.JSON200.OnBoard {
		t.Fatal("setup restored removed membership")
	}
}
