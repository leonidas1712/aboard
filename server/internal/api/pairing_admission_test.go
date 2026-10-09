package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestPairingNonmemberWaitsForItsExactAdmissionApproval(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name := s.newBoard()
	a, at := s.joinAs(s.owner, name, "writer", "")
	seat := s.memberNamed(at, name, a)
	maya := s.addHuman("maya")
	me, err := s.client(maya).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	disabled := false
	gate, err := s.client(s.owner).UpdateBoardWithResponse(ctx, name, nil, api.UpdateBoardRequest{AgentsAddPeople: &disabled})
	mustStatus(t, gate, err, 200)
	key := "pairing-held-admission"
	in := api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: seat.Id, Work: "Review together after admission."}
	created, err := s.client(at).CreatePairingRequestWithResponse(ctx, &api.CreatePairingRequestParams{IdempotencyKey: &key}, in)
	mustStatus(t, created, err, 201)
	if created.JSON201.State != "awaiting_endpoint" || created.JSON201.Recipient != nil || created.JSON201.Initiator != nil || created.JSON201.Next == nil {
		t.Fatalf("held admission selected an endpoint or omitted its handover: %s", created.Body)
	}
	before, err := s.client(maya).GetBoardWithResponse(ctx, name)
	mustStatus(t, before, err, 200)
	if before.JSON200.OnBoard {
		t.Fatal("held approval admitted the person")
	}
	approvals, err := s.client(s.owner).ListApprovalsWithResponse(ctx)
	mustStatus(t, approvals, err, 200)
	var exact *api.Approval
	for _, approval := range approvals.JSON200.Approvals {
		action, e := approval.Action.AsAddPeopleAction()
		if e == nil && action.Kind == "add_people" && action.BoardId == b.JSON200.Id && action.PersonId == me.JSON200.Id {
			copy := approval
			exact = &copy
		}
	}
	if exact == nil || exact.State != "pending" || !strings.Contains(created.JSON201.Next.Command, exact.Id) {
		t.Fatalf("missing exact pending admission approval: %s / %s", created.Body, approvals.Body)
	}
	denied, err := s.client(maya).CreatePairingCredentialWithResponse(ctx, nil, api.CreatePairingCredential{RequestId: created.JSON201.Id, Side: "recipient", AgentId: seat.Id, SessionBinding: "sha256:" + strings.Repeat("a", 64), Generation: 1})
	if err != nil || denied.StatusCode() < 400 {
		t.Fatalf("unadmitted recipient minted an endpoint: %v %s", err, denied.Body)
	}
	allowed, err := s.client(s.owner).AllowApprovalWithResponse(ctx, exact.Id, nil, api.AllowApprovalRequest{})
	mustStatus(t, allowed, err, 200)
	resumed, err := s.client(at).GetPairingRequestWithResponse(ctx, created.JSON201.Id)
	mustStatus(t, resumed, err, 200)
	if resumed.JSON200.Id != created.JSON201.Id || resumed.JSON200.Recipient != nil || resumed.JSON200.State == "ready" {
		t.Fatalf("approval selected a session or changed request: %s", resumed.Body)
	}
	after, err := s.client(maya).GetBoardWithResponse(ctx, name)
	mustStatus(t, after, err, 200)
	if !after.JSON200.OnBoard {
		t.Fatal("executed admission did not admit recipient")
	}
	repeated, err := s.client(at).CreatePairingRequestWithResponse(ctx, &api.CreatePairingRequestParams{IdempotencyKey: &key}, in)
	mustStatus(t, repeated, err, 201)
	if repeated.JSON201.Id != created.JSON201.Id {
		t.Fatal("replay created another pairing")
	}
}

func TestPairingAllowanceAdmitsTheRecipientWithoutSelectingTheirSession(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name := s.newBoard()
	a, at := s.joinAs(s.owner, name, "writer", "")
	seat := s.memberNamed(at, name, a)
	maya := s.addHuman("maya")
	me, err := s.client(maya).GetMeWithResponse(ctx)
	mustStatus(t, me, err, 200)
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, b, err, 200)
	disabled := false
	gate, err := s.client(s.owner).UpdateBoardWithResponse(ctx, name, nil, api.UpdateBoardRequest{AgentsAddPeople: &disabled})
	mustStatus(t, gate, err, 200)
	allowance, err := s.client(s.owner).SetAllowanceWithResponse(ctx, nil, api.SetAllowanceRequest{Categories: []api.AllowanceCategory{"add-people"}})
	mustStatus(t, allowance, err, 200)
	created, err := s.client(at).CreatePairingRequestWithResponse(ctx, nil, api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: seat.Id, Work: "Review together."})
	mustStatus(t, created, err, 201)
	if created.JSON201.Recipient != nil || created.JSON201.Initiator != nil || created.JSON201.State == "ready" {
		t.Fatalf("allowance picked a runtime: %s", created.Body)
	}
	after, err := s.client(maya).GetBoardWithResponse(ctx, name)
	mustStatus(t, after, err, 200)
	if !after.JSON200.OnBoard {
		t.Fatal("permitted allowance did not admit recipient")
	}
}

func TestPairingAdmissionCannotResumeAfterCancellationOrInitiatorRemoval(t *testing.T) {
	for _, removed := range []bool{false, true} {
		nameCase := "cancelled"
		if removed {
			nameCase = "initiator_removed"
		}
		t.Run(nameCase, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			ctx := context.Background()
			name := s.newBoard()
			a, at := s.joinAs(s.owner, name, "writer", "")
			seat := s.memberNamed(at, name, a)
			maya := s.addHuman("maya")
			me, err := s.client(maya).GetMeWithResponse(ctx)
			mustStatus(t, me, err, 200)
			b, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
			mustStatus(t, b, err, 200)
			disabled := false
			gate, err := s.client(s.owner).UpdateBoardWithResponse(ctx, name, nil, api.UpdateBoardRequest{AgentsAddPeople: &disabled})
			mustStatus(t, gate, err, 200)
			created, err := s.client(at).CreatePairingRequestWithResponse(ctx, nil, api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: me.JSON200.Id, InitiatingAgentId: seat.Id, Work: "Wait for admission."})
			mustStatus(t, created, err, 201)
			if removed {
				ended, e := s.client(s.owner).RemoveAgentWithResponse(ctx, name, seat.Name, nil)
				mustStatus(t, ended, e, 200)
				resumed, e := s.client(at).GetPairingRequestWithResponse(ctx, created.JSON201.Id)
				if e != nil || resumed.StatusCode() < 400 {
					t.Fatalf("removed initiator resumed pairing: %v %s", e, resumed.Body)
				}
			} else {
				cancelled, e := s.client(s.owner).CancelPairingRequestWithResponse(ctx, created.JSON201.Id, nil)
				mustStatus(t, cancelled, e, 200)
				resumed, e := s.client(s.owner).GetPairingRequestWithResponse(ctx, created.JSON201.Id)
				mustStatus(t, resumed, e, 200)
				if resumed.JSON200.State != "cancelled" || resumed.JSON200.Recipient != nil {
					t.Fatalf("cancelled pairing resumed: %s", resumed.Body)
				}
			}
			after, err := s.client(maya).GetBoardWithResponse(ctx, name)
			mustStatus(t, after, err, 200)
			if after.JSON200.OnBoard {
				t.Fatal("closed or unauthorized pairing admitted recipient")
			}
		})
	}
}
