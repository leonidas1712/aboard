package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func displayBoards(boards []board.OnboardingBoardDisplay) []map[string]any {
	out := make([]map[string]any, 0, len(boards))
	for _, b := range boards {
		out = append(out, map[string]any{"id": b.ID, "name": b.Name, "title": b.Title})
	}
	return out
}

func (h *handlers) onboardingDisplay(ctx context.Context, personID, agentID string, boards []string) map[string]any {
	labels, err := h.svc.OnboardingLabels(ctx, principal(ctx), personID, agentID, boards)
	out := map[string]any{"boards": []map[string]any{}}
	// The action may already have committed. Lost access omits labels rather than
	// failing its response or disclosing metadata from the earlier snapshot.
	if err != nil {
		return out
	}
	out["boards"] = displayBoards(labels.Boards)
	if labels.PersonHandle != "" {
		out["person_handle"] = labels.PersonHandle
	}
	if labels.AgentName != "" {
		out["agent_name"] = labels.AgentName
	}
	if labels.AgentHarness != "" {
		out["agent_harness"] = labels.AgentHarness
	}
	return out
}

func (h *handlers) PreviewServerInvite(ctx context.Context, req PreviewServerInviteRequestObject) (PreviewServerInviteResponseObject, error) {
	preview, err := h.svc.PreviewServerInvite(ctx, req.Body.Invite)
	if err != nil {
		return nil, err
	}
	cfg := h.svc.Config()
	out := map[string]any{"server_id": cfg.ServerID, "server_url": cfg.IssuerURL, "server_name": "aboard", "inviter_handle": preview.InviterHandle, "boards": displayBoards(preview.Boards), "expires_at": preview.ExpiresAt}
	if preview.Work != "" {
		out["work"] = preview.Work
	}
	return convert[PreviewServerInvite200JSONResponse](out)
}
