package board_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/events"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestApprovalExecutesExactActionOnceAndKeepsAuthorityCurrent(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	joined, err := w.svc.Join(ctx, w.maya, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	view, err := w.svc.GetBoard(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	action := board.AdminAction{Kind: "add_people", BoardID: view.Board.ID, PersonID: w.alex.Human.ID}
	held, err := w.svc.RequestAdminAction(ctx, agent, action, "request-1")
	if err != nil || held.State != "pending" {
		t.Fatalf("request: %+v %v", held, err)
	}
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		workers.Go(func() { _, err := w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, false); errs <- err })
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	approvals, err := w.svc.ListApprovals(ctx, agent)
	if err != nil || len(approvals) != 1 || approvals[0].Execution == nil {
		t.Fatalf("audit: %+v %v", approvals, err)
	}
	if _, err := w.svc.SetAllowance(ctx, w.maya, []string{"revoke-keys"}); err == nil {
		t.Fatal("destructive allowance accepted")
	}
	held, err = w.svc.RequestAdminAction(ctx, agent, board.AdminAction{Kind: "set_board_role", BoardID: view.Board.ID, PersonID: w.sam.Human.ID, Role: "owner"}, "request-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, true); err == nil {
		t.Fatal("always allowed privilege raising")
	}
	if _, err := w.svc.RevokeKey(ctx, w.maya, agent.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, false); err == nil {
		t.Fatal("revoked agent parent executed")
	}
}

func TestAllowanceReplayNeverRetrievesInviteSecretOrRestoresAuthority(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "alex"); err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.alex, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	if _, err := w.svc.SetAllowance(ctx, w.alex, []string{"invite-people"}); err != nil {
		t.Fatal(err)
	}
	action := board.AdminAction{Kind: "invite_people", Invite: &board.InvitePeopleInput{}}
	first, err := w.svc.RequestAdminAction(ctx, agent, action, "lost-invite-response")
	if err != nil || first.Invite == nil || first.Invite.Secret == "" {
		t.Fatalf("initial invite: %+v %v", first, err)
	}
	retry, err := w.svc.RequestAdminAction(ctx, agent, action, "lost-invite-response")
	if err != nil || retry.Approval.ID != first.Approval.ID || retry.Invite != nil {
		t.Fatalf("secret replay: %+v %v", retry, err)
	}
	if _, err := w.svc.SetAllowance(ctx, w.alex, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.RequestAdminAction(ctx, agent, action, "lost-invite-response"); err == nil {
		t.Fatal("revoked allowance replayed")
	}
	bundled := board.AdminAction{Kind: "invite_people", Invite: &board.InvitePeopleInput{Boards: []string{"brd_private"}}}
	_, err = w.svc.RequestAdminAction(ctx, agent, bundled, "not-partial")
	wantCode(t, "bundled invite", err, "not_implemented")
}

func TestApprovedServerRemovalRetainsNonsecretExecutionHistory(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "alex"); err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.alex, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	held, err := w.svc.RequestAdminAction(ctx, agent, board.AdminAction{Kind: "remove_person", PersonID: w.sam.Human.ID}, "remove-sam")
	if err != nil {
		t.Fatal(err)
	}
	executed, err := w.svc.AllowApproval(ctx, w.alex, held.Approval.ID, false)
	if err != nil || executed.State != "executed" {
		t.Fatalf("remove: %+v %v", executed, err)
	}
	approvals, err := w.svc.ListApprovals(ctx, w.alex)
	if err != nil || len(approvals) != 1 || approvals[0].Execution == nil {
		t.Fatalf("removal audit disappeared: %+v %v", approvals, err)
	}
}

func TestOrdinaryAdmissionAllowanceAndExactPrivilegeDecisions(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	joined, err := w.svc.Join(ctx, w.maya, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	view, err := w.svc.GetBoard(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	a := board.AdminAction{Kind: "add_people", BoardID: view.Board.ID, PersonID: w.alex.Human.ID}
	if _, err := w.svc.SetAllowance(ctx, w.maya, []string{"add-people"}); err != nil {
		t.Fatal(err)
	}
	out, err := w.svc.RequestAdminAction(ctx, agent, a, "admit-alex")
	if err != nil || out.State != "executed" || out.Approval.Execution.Authorization.Via != "allowance" {
		t.Fatalf("admission: %+v %v", out, err)
	}
	a.Role = "owner"
	if _, err = w.svc.RequestAdminAction(ctx, agent, a, "smuggled-role"); err == nil {
		t.Fatal("extra privilege field accepted")
	}
	promote := board.AdminAction{Kind: "set_board_role", BoardID: view.Board.ID, PersonID: w.sam.Human.ID, Role: "owner"}
	held, err := w.svc.RequestAdminAction(ctx, agent, promote, "promote-sam")
	if err != nil || held.State != "pending" {
		t.Fatalf("privilege request: %+v %v", held, err)
	}
	if _, err = w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, false); err != nil {
		t.Fatal(err)
	}
	promote.Role = "member"
	held, err = w.svc.RequestAdminAction(ctx, agent, promote, "demote-sam")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, false); err != nil {
		t.Fatal(err)
	}
	removal := board.AdminAction{Kind: "remove_person", BoardID: view.Board.ID, PersonID: w.alex.Human.ID}
	held, err = w.svc.RequestAdminAction(ctx, agent, removal, "decline-removal")
	if err != nil {
		t.Fatal(err)
	}
	declined, err := w.svc.DeclineApproval(ctx, w.maya, held.Approval.ID)
	if err != nil || declined.State != "declined" {
		t.Fatalf("decline: %+v %v", declined, err)
	}
	if _, err = w.svc.AllowApproval(ctx, w.maya, held.Approval.ID, false); err == nil {
		t.Fatal("declined approval executed")
	}
	entries, err := w.svc.Events(ctx, w.maya, w.board, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	verifier := events.NewVerifier()
	found := false
	for _, e := range entries.Events {
		if problem := verifier.Add([]events.Event{e}); problem != nil {
			t.Fatalf("chain: %+v", problem)
		}
		if e.Type == events.PersonRoleChanged {
			var data map[string]any
			if err = json.Unmarshal(e.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data["before"] != "owner" || data["after"] != "member" || data["authorization"] == nil {
				t.Fatalf("demotion record: %s", e.Data)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("demotion was not recorded")
	}
}

func TestAdminRequestKeysExpireAtTwentyFourHoursWithoutDeletingAudit(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.AddPerson(ctx, w.maya, w.board, "alex"); err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.alex, board.JoinInput{Board: w.board, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	if _, err := w.svc.SetAllowance(ctx, w.alex, []string{"invite-people"}); err != nil {
		t.Fatal(err)
	}
	a := board.AdminAction{Kind: "invite_people", Invite: &board.InvitePeopleInput{}}
	first, err := w.svc.RequestAdminAction(ctx, agent, a, "retained-key")
	if err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(24*time.Hour - time.Millisecond)
	repeated, err := w.svc.RequestAdminAction(ctx, agent, a, "retained-key")
	if err != nil || repeated.Approval.ID != first.Approval.ID || repeated.Invite != nil {
		t.Fatalf("before expiry: %+v %v", repeated, err)
	}
	w.clk.Advance(time.Millisecond)
	next, err := w.svc.RequestAdminAction(ctx, agent, a, "retained-key")
	if err != nil || next.Approval.ID == first.Approval.ID || next.Invite == nil {
		t.Fatalf("at expiry: %+v %v", next, err)
	}
	list, err := w.svc.ListApprovals(ctx, w.alex)
	if err != nil || len(list) != 2 {
		t.Fatalf("immutable history: %+v %v", list, err)
	}
}

func TestDeclineApprovalDoesNotRevealBoardAfterAccessLoss(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	other, err := w.svc.CreateBoard(ctx, w.maya, board.NewBoard{Template: "general", Visibility: board.BoardPrivate})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.maya, board.JoinInput{Board: other.Board.Name, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	view, err := w.svc.GetBoard(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	action := board.AdminAction{Kind: "add_people", BoardID: view.Board.ID, PersonID: w.alex.Human.ID}
	pending, err := w.svc.RequestAdminAction(ctx, agent, action, "private-pending")
	if err != nil {
		t.Fatal(err)
	}
	declined, err := w.svc.RequestAdminAction(ctx, agent, action, "private-declined")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.DeclineApproval(ctx, w.maya, declined.Approval.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = w.svc.MakeOwner(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.Leave(ctx, w.maya, w.board); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{pending.Approval.ID, declined.Approval.ID} {
		_, err = w.svc.DeclineApproval(ctx, w.maya, id)
		wantCode(t, "hidden decline", err, "approval_not_found")
	}
	list, err := w.svc.ListApprovals(ctx, w.maya)
	if err != nil || len(list) != 0 {
		t.Fatalf("hidden list: %+v %v", list, err)
	}
}

func TestTransactionAdmissionKeepsExistingAgentAddAuthority(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	yes := true
	if _, err := w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{AgentsAddPeople: &yes}); err != nil {
		t.Fatal(err)
	}
	view, err := w.svc.GetBoard(ctx, w.maya, w.board)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.Join(ctx, w.sam, board.JoinInput{Board: w.board, Role: "member", Session: "codex:transaction-add", Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	agent := w.auth(ctx, t, joined.Token)
	var out board.AdminActionResult
	err = w.gate.Write(ctx, func(tx board.Tx) error {
		var err error
		out, err = w.svc.RequestAddPersonTx(ctx, tx, agent, view.Board.ID, w.alex.Human.ID)
		return err
	})
	if err != nil || out.State != "executed" || out.Approval.ID != "" {
		t.Fatalf("direct admission: %+v %v", out, err)
	}
	w.svc.NotifyAdminResult(out)
	if _, err := w.svc.GetBoard(ctx, w.alex, w.board); err != nil {
		t.Fatal(err)
	}
	list, err := w.svc.ListApprovals(ctx, w.sam)
	if err != nil || len(list) != 0 {
		t.Fatalf("legacy admission unexpectedly held: %+v %v", list, err)
	}
}
