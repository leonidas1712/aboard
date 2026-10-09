package api

import "context"

func (h *handlers) ListInviteNotices(ctx context.Context, _ ListInviteNoticesRequestObject) (ListInviteNoticesResponseObject, error) {
	list, err := h.svc.ListInviteNotices(ctx, principal(ctx))
	if err != nil {
		return nil, onboardingError(ctx, err, "aboard invite list")
	}
	notices := make([]map[string]any, 0, len(list))
	for _, v := range list {
		i := v.Invite
		notices = append(notices, map[string]any{
			"id": i.ID, "issuing_agent_id": i.IssuingAgentID,
			"message": "Your agent invited someone", "created_at": i.CreatedAt,
			"expires_at": i.ExpiresAt, "state": v.State,
			"next": map[string]any{
				"command": onboardingCommand(ctx, "aboard invite revoke '"+i.ID+"'"),
				"resume":  "Revoke an unredeemed invite in your terminal. Redeemed people and memberships remain.",
			},
		})
	}
	return convert[ListInviteNotices200JSONResponse](map[string]any{"notices": notices})
}
