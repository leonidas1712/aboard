package api

import (
	"context"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

// RemoveAgent removes one agent from a board for good.
func (h *handlers) RemoveAgent(ctx context.Context, req RemoveAgentRequestObject) (RemoveAgentResponseObject, error) {
	r, err := h.svc.RemoveAgent(ctx, principal(ctx), req.Board, req.Member)
	if err != nil {
		return nil, err
	}
	return convert[RemoveAgent200JSONResponse](removedAgentOf(r))
}

// LeaveAsAgent removes the calling agent's own seat.
func (h *handlers) LeaveAsAgent(ctx context.Context, _ LeaveAsAgentRequestObject) (LeaveAsAgentResponseObject, error) {
	r, err := h.svc.LeaveAsAgent(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return convert[LeaveAsAgent200JSONResponse](removedAgentOf(r))
}

// PruneAgents lists or removes agents disconnected for a while.
func (h *handlers) PruneAgents(ctx context.Context, req PruneAgentsRequestObject) (PruneAgentsResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.New(http.StatusBadRequest, "invalid_request", "The request needs a body.", "Send {\"disconnected_for\": 604800, \"dry_run\": true}.")
	}
	in := board.PruneInput{DisconnectedFor: time.Duration(req.Body.DisconnectedFor) * time.Second}
	if req.Body.All != nil {
		in.All = *req.Body.All
	}
	if req.Body.DryRun != nil {
		in.DryRun = *req.Body.DryRun
	}
	if req.Body.Agents != nil {
		in.Agents = *req.Body.Agents
	}
	r, err := h.svc.PruneAgents(ctx, principal(ctx), in)
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(r.Agents))
	for _, a := range r.Agents {
		var name, boardName any = a.Member.Name, a.Board.Name
		if a.Hidden {
			name, boardName = nil, nil
		}
		agents = append(agents, map[string]any{
			"id": a.Member.ID, "name": name, "board": boardName, "board_id": a.Board.ID,
			"owner": a.Owner.Name, "owner_id": a.Owner.ID, "disconnected_since": a.Since,
		})
	}
	return convert[PruneAgents200JSONResponse](map[string]any{
		"disconnected_for": req.Body.DisconnectedFor, "all": in.All, "dry_run": in.DryRun,
		"agents": agents, "kept": r.Kept,
	})
}

// removedAgentOf is an agent whose seat ended, as the API shows it.
func removedAgentOf(r board.RemovedAgent) map[string]any {
	var name, boardName any = r.Member.Name, r.Board.Name
	if r.Hidden {
		name, boardName = nil, nil
	}
	status := board.StatusRemoved
	if r.Member.Status == board.StatusLeft {
		status = board.StatusLeft
	}
	return map[string]any{
		"id": r.Member.ID, "board": boardName, "board_id": r.Board.ID, "name": name,
		"owner": r.Owner.Name, "owner_id": r.Owner.ID, "status": status,
		"removed_at": r.Member.RemovedAt, "removed_by": r.Member.RemovedBy,
	}
}
