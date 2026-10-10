package api

import "context"

func (h *handlers) ListServerInvites(ctx context.Context, _ ListServerInvitesRequestObject) (ListServerInvitesResponseObject, error) {
	list, err := h.svc.ListServerInvites(ctx, principal(ctx))
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard invite list")
	}
	invites := make([]map[string]any, 0, len(list))
	for _, v := range list {
		i := v.Invite
		entry := map[string]any{"id": i.ID, "created_at": i.CreatedAt, "expires_at": i.ExpiresAt, "state": v.State, "boards": i.Boards}
		entry["display"] = h.onboardingDisplay(ctx, i.CreatedBy, i.IssuingAgentID, i.Boards)
		if i.PairingRequestID != "" {
			entry["pairing_request_id"] = i.PairingRequestID
		}
		if i.ParentKeyID != "" {
			entry["parent_key_id"] = i.ParentKeyID
		}
		if i.SuggestedHandle != "" {
			entry["suggested_handle"] = i.SuggestedHandle
		}
		if i.IssuingAgentID != "" {
			entry["issuing_agent_id"] = i.IssuingAgentID
		}
		invites = append(invites, entry)
	}
	return convert[ListServerInvites200JSONResponse](map[string]any{"invites": invites})
}

func (h *handlers) RevokeServerInvite(ctx context.Context, req RevokeServerInviteRequestObject) (RevokeServerInviteResponseObject, error) {
	changed, err := h.svc.RevokeServerInvite(ctx, principal(ctx), req.Invite)
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard invite revoke '"+req.Invite+"'")
	}
	return convert[RevokeServerInvite200JSONResponse](map[string]any{"id": req.Invite, "revoked": true, "changed": changed})
}
