package api

import (
	"context"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/board"
)

const minimumSetupClient = "0.1.4"

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

func (h *handlers) serverInviteOf(inv board.NewServerInvite) map[string]any {
	out := map[string]any{"id": inv.Invite.ID, "invite": inv.Secret, "server_role": board.ServerMember, "expires_at": inv.Invite.ExpiresAt}
	if issuer := strings.TrimRight(h.svc.Config().IssuerURL, "/"); issuer != "" {
		link := issuer + "/join#" + inv.Secret
		out["link"] = link
		out["prompt"] = invitationPrompt(link, inv.Invite.SuggestedHandle, inv.Invite.PairingRequestID != "")
	}
	if inv.Invite.SuggestedHandle != "" {
		out["suggested_handle"] = inv.Invite.SuggestedHandle
	}
	if inv.Invite.Boards != nil {
		out["boards"] = inv.Invite.Boards
	}
	if inv.Invite.PairingRequestID != "" {
		out["pairing_request_id"] = inv.Invite.PairingRequestID
	}
	return out
}

func invitationPrompt(link, handle string, _ bool) string {
	if handle == "" {
		handle = "<name you'd like teammates to see>"
	}
	prompt := "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh. If aboard version is older than " + minimumSetupClient + " and is not a +dev build, run aboard upgrade first. Run aboard skill, then run aboard setup " + link + " --handle " + handle + "."
	return prompt + " Run Aboard outside your agent's sandbox; approve it when your harness asks."
}
