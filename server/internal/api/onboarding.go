package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func onboardingOf(r board.OnboardingReceipt) map[string]any {
	out := map[string]any{"server_id": r.ServerID, "person_id": r.PersonID, "key_id": r.KeyID, "invite_id": r.InviteID, "handle": r.Handle, "boards": r.Boards}
	if r.PairingRequestID != "" {
		out["pairing_request_id"] = r.PairingRequestID
	}
	return out
}

func (h *handlers) GetOnboardingReceipt(ctx context.Context, _ GetOnboardingReceiptRequestObject) (GetOnboardingReceiptResponseObject, error) {
	r, err := h.svc.GetOnboardingReceipt(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return convert[GetOnboardingReceipt200JSONResponse](onboardingOf(r))
}

func serverInviteOf(inv board.NewServerInvite) map[string]any {
	out := map[string]any{"id": inv.Invite.ID, "invite": inv.Secret, "server_role": board.ServerMember, "expires_at": inv.Invite.ExpiresAt}
	if inv.Invite.Boards != nil {
		out["boards"] = inv.Invite.Boards
	}
	if inv.Invite.PairingRequestID != "" {
		out["pairing_request_id"] = inv.Invite.PairingRequestID
	}
	return out
}
