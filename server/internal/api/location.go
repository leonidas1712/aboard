package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func locationOf(loc *board.AgentLocation) any {
	if loc == nil {
		return nil
	}
	out := map[string]any{"harness": loc.Harness, "session_id": loc.SessionID, "folder": loc.Folder, "last_active": loc.LastActive}
	if loc.Machine != "" {
		out["machine"] = loc.Machine
	}
	return out
}

func (h *handlers) ListOwnAgents(ctx context.Context, _ ListOwnAgentsRequestObject) (ListOwnAgentsResponseObject, error) {
	agents, err := h.svc.ListOwnAgents(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := []wireMember{}
	for _, agent := range agents {
		out = append(out, memberOf(agent.Member, agent.BoardName))
	}
	return convert[ListOwnAgents200JSONResponse](map[string]any{"agents": out})
}

func (h *handlers) SetAgentLocation(ctx context.Context, req SetAgentLocationRequestObject) (SetAgentLocationResponseObject, error) {
	in := req.Body
	loc, err := h.svc.SetAgentLocation(ctx, principal(ctx), board.AgentLocation{Harness: in.Harness, SessionID: in.SessionId, Folder: in.Folder})
	if err != nil {
		return nil, err
	}
	return convert[SetAgentLocation200JSONResponse](locationOf(&loc))
}
