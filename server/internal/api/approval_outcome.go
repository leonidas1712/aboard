package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (h *handlers) approvalOutcomeOf(ctx context.Context, out board.ApprovalOutcome) map[string]any {
	wire := map[string]any{"approval": h.approvalOf(ctx, out.Approval), "collected": out.Collected}
	if out.PairingRequestID != "" {
		wire["pairing_request_id"] = out.PairingRequestID
	}
	if out.Invite != nil {
		wire["invite"] = h.serverInviteOf(*out.Invite)
	}
	if out.Approval.State == "pending" {
		wire["next"] = approvalNext(ctx, out.Approval.ID)
	}
	return wire
}

func (h *handlers) GetApproval(ctx context.Context, req GetApprovalRequestObject) (GetApprovalResponseObject, error) {
	out, err := h.svc.GetApproval(ctx, principal(ctx), req.Approval)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard approvals show "+req.Approval)
	}
	return convert[GetApproval200JSONResponse](h.approvalOutcomeOf(ctx, out))
}

func (h *handlers) CollectApproval(ctx context.Context, req CollectApprovalRequestObject) (CollectApprovalResponseObject, error) {
	key := ""
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	out, err := h.svc.CollectApproval(ctx, principal(ctx), req.Approval, key)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard approvals show "+req.Approval)
	}
	return convert[CollectApproval200JSONResponse](h.approvalOutcomeOf(ctx, out))
}
