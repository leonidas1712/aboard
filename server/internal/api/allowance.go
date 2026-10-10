package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

type onboardingIssuerKey struct{}

const inviteAllowanceWarning = "Agents allowed to invite people can let outsiders read every open board."

func onboardingCommand(ctx context.Context, command string) string {
	issuer, _ := ctx.Value(onboardingIssuerKey{}).(string)
	return command + " --server '" + strings.ReplaceAll(issuer, "'", "'\\''") + "'"
}

func onboardingError(ctx context.Context, err error, command string) error {
	if e, ok := apierr.As(err); ok && e.Code == "human_token_required" {
		refusal := *e
		refusal.Next = map[string]any{"command": onboardingCommand(ctx, command), "resume": "Continue after your person decides."}
		return &refusal
	}
	return err
}

func allowanceOf(a board.Allowance) map[string]any {
	categories := a.Categories
	if categories == nil {
		categories = []string{}
	}
	out := map[string]any{"id": a.ID, "person_id": a.PersonID, "revision": a.Revision, "categories": categories}
	for _, category := range categories {
		if category == "invite-people" {
			out["warning"] = inviteAllowanceWarning
			break
		}
	}
	return out
}

func (h *handlers) GetAllowance(ctx context.Context, _ GetAllowanceRequestObject) (GetAllowanceResponseObject, error) {
	a, err := h.svc.GetAllowance(ctx, principal(ctx))
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard allowance")
	}
	return convert[GetAllowance200JSONResponse](allowanceOf(a))
}

func (h *handlers) SetAllowance(ctx context.Context, req SetAllowanceRequestObject) (SetAllowanceResponseObject, error) {
	categories := make([]string, 0, len(req.Body.Categories))
	for _, category := range req.Body.Categories {
		categories = append(categories, string(category))
	}
	a, err := h.svc.SetAllowance(ctx, principal(ctx), categories)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard allowance")
	}
	return convert[SetAllowance200JSONResponse](allowanceOf(a))
}

func (h *handlers) approvalOf(ctx context.Context, a board.Approval) map[string]any {
	out := map[string]any{
		"id": a.ID, "person_id": a.PersonID, "agent_id": a.AgentID,
		"parent_key_id": a.ParentKeyID, "payload_hash": a.PayloadHash, "action": board.AdminActionJSON(a.Action),
		"state": a.State, "created_at": a.CreatedAt,
	}
	boards := []string{}
	if a.Action.BoardID != "" {
		boards = append(boards, a.Action.BoardID)
	}
	if a.Action.Invite != nil {
		boards = append(boards, a.Action.Invite.Boards...)
	}
	display := h.onboardingDisplay(ctx, a.PersonID, a.AgentID, boards)
	if a.Action.PersonID != "" {
		if target, err := h.svc.OnboardingLabels(ctx, principal(ctx), a.Action.PersonID, "", nil); err == nil && target.PersonHandle != "" {
			display["target_handle"] = target.PersonHandle
		}
	}
	if a.Action.Kind == "revoke_key" {
		if name, err := h.svc.ApprovalKeyName(ctx, principal(ctx), a.ID); err == nil && name != "" {
			display["key_name"] = name
		}
	}
	out["display"] = display
	if a.Execution != nil && a.Execution.Decision != "" {
		out["decision"] = a.Execution.Decision
	}
	if a.ExpiresAt != nil {
		out["expires_at"] = a.ExpiresAt
	}
	if a.DecidedAt != nil {
		out["decided_at"] = a.DecidedAt
	}
	if a.State == "pending" {
		out["next"] = approvalNext(ctx, a.ID)
	}
	if a.Execution != nil {
		e := a.Execution
		auth := e.Authorization
		provenance := map[string]any{
			"person_id": auth.PersonID, "agent_id": auth.AgentID,
			"parent_key_id": auth.ParentKeyID, "payload_hash": auth.PayloadHash, "via": auth.Via,
		}
		if auth.Kind != "" {
			provenance["kind"] = auth.Kind
		}
		if auth.AllowanceID != "" {
			provenance["allowance_id"] = auth.AllowanceID
			provenance["allowance_revision"] = auth.AllowanceRevision
		}
		if auth.ApprovalID != "" {
			provenance["approval_id"] = auth.ApprovalID
		}
		execution := map[string]any{"at": e.At, "authorization": provenance}
		if e.InviteID != "" {
			execution["invite_id"] = e.InviteID
		}
		out["execution"] = execution
	}
	return out
}

func approvalNext(ctx context.Context, id string) map[string]any {
	return map[string]any{"command": onboardingCommand(ctx, "aboard approvals allow "+id), "resume": "Continue after your person allows or declines this exact action."}
}

func (h *handlers) adminResultOf(ctx context.Context, r board.AdminActionResult) map[string]any {
	out := map[string]any{"state": r.State, "approval": h.approvalOf(ctx, r.Approval)}
	if r.State == "pending" {
		out["next"] = approvalNext(ctx, r.Approval.ID)
	}
	if r.Invite != nil {
		out["invite"] = h.serverInviteOf(*r.Invite)
	}
	return out
}

func (h *handlers) ListApprovals(ctx context.Context, req ListApprovalsRequestObject) (ListApprovalsResponseObject, error) {
	list, err := h.svc.ListApprovals(ctx, principal(ctx), approvalState(req.Params.State))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		out = append(out, h.approvalOf(ctx, a))
	}
	return convert[ListApprovals200JSONResponse](map[string]any{"approvals": out})
}

func decodeAdminAction(action AdminAction) (board.AdminAction, error) {
	data, err := json.Marshal(action)
	if err != nil {
		return board.AdminAction{}, err
	}
	var wire struct {
		Kind     string `json:"kind"`
		BoardID  string `json:"board_id"`
		PersonID string `json:"person_id"`
		Role     string `json:"role"`
		KeyID    string `json:"key_id"`
		Invite   *struct {
			SuggestedHandle string   `json:"suggested_handle"`
			TTLSeconds      *int     `json:"ttl_seconds"`
			Boards          []string `json:"boards"`
			Pairing         *struct {
				InitiatingAgentID string `json:"initiating_agent_id"`
				Work              string `json:"work"`
			} `json:"pairing"`
		} `json:"invite"`
		Policy *rules.PolicyChange `json:"policy"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return board.AdminAction{}, fmt.Errorf("decode admin action: %w", err)
	}
	out := board.AdminAction{Kind: wire.Kind, BoardID: wire.BoardID, PersonID: wire.PersonID, Role: wire.Role, KeyID: wire.KeyID, Policy: wire.Policy}
	if wire.Invite != nil {

		out.Invite = &board.InvitePeopleInput{SuggestedHandle: wire.Invite.SuggestedHandle, TTLSeconds: wire.Invite.TTLSeconds, Boards: wire.Invite.Boards}
		if wire.Invite.Pairing != nil {
			out.Invite.Pairing = &board.InvitePairingInput{InitiatingAgentID: wire.Invite.Pairing.InitiatingAgentID, Work: wire.Invite.Pairing.Work}
		}
	}
	return out, nil
}

func (h *handlers) RequestAdminAction(ctx context.Context, req RequestAdminActionRequestObject) (RequestAdminActionResponseObject, error) {
	action, err := decodeAdminAction(*req.Body)
	if err != nil {
		return nil, err
	}
	key := ""
	if req.Params.IdempotencyKey != nil {
		key = string(*req.Params.IdempotencyKey)
	}
	r, err := h.svc.RequestAdminAction(ctx, principal(ctx), action, key)
	if err != nil {
		return nil, err
	}
	out := h.adminResultOf(ctx, r)
	if r.State == "pending" {
		return convert[RequestAdminAction202JSONResponse](out)
	}
	if r.Invite != nil {
		return convert[RequestAdminAction201JSONResponse](out)
	}
	return convert[RequestAdminAction200JSONResponse](out)
}

func (h *handlers) AllowApproval(ctx context.Context, req AllowApprovalRequestObject) (AllowApprovalResponseObject, error) {
	always := req.Body != nil && req.Body.Always != nil && *req.Body.Always
	r, err := h.svc.AllowApproval(ctx, principal(ctx), req.Approval, always)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard approvals allow "+req.Approval)
	}
	out := h.adminResultOf(ctx, r)
	if always && r.Approval.Action.Kind == "invite_people" {
		out["warning"] = inviteAllowanceWarning
	}
	return convert[AllowApproval200JSONResponse](out)
}

func (h *handlers) DeclineApproval(ctx context.Context, req DeclineApprovalRequestObject) (DeclineApprovalResponseObject, error) {
	a, err := h.svc.DeclineApproval(ctx, principal(ctx), req.Approval)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard approvals decline "+req.Approval)
	}
	return convert[DeclineApproval200JSONResponse](h.approvalOf(ctx, a))
}

func approvalState(state *ListApprovalsParamsState) string {
	if state == nil {
		return "all"
	}
	return string(*state)
}
