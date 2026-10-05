package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// boardPersonOf is a person on a board as the API shows them.
func boardPersonOf(bp board.Person, boardName string) map[string]any {
	role := "member"
	if bp.IsOwner() {
		role = "owner"
	}
	return map[string]any{
		"id": bp.Person.ID, "handle": bp.Person.Name, "display_name": bp.Person.DisplayName, "board": boardName,
		"name": bp.Member.Name, "member_id": bp.Member.ID, "board_role": role, "joined_at": bp.Member.JoinedAt,
	}
}

// ListPeople lists the people on a board the caller can see.
func (h *handlers) ListPeople(ctx context.Context, req ListPeopleRequestObject) (ListPeopleResponseObject, error) {
	bp, err := h.svc.People(ctx, principal(ctx), req.Board)
	if err != nil {
		return nil, err
	}
	people := make([]map[string]any, 0, len(bp.People))
	for _, p := range bp.People {
		people = append(people, boardPersonOf(p, bp.Board.Name))
	}
	return convert[ListPeople200JSONResponse](map[string]any{"board": bp.Board.Name, "visibility": bp.Board.Visibility, "people": people})
}

// AddPerson adds a person on the server to a board.
func (h *handlers) AddPerson(ctx context.Context, req AddPersonRequestObject) (AddPersonResponseObject, error) {
	bp, err := h.svc.AddPerson(ctx, principal(ctx), req.Board, req.Body.Handle)
	if err != nil {
		return nil, err
	}
	return convert[AddPerson201JSONResponse](boardPersonOf(bp, req.Board))
}

// RemovePerson takes a person off a board.
func (h *handlers) RemovePerson(ctx context.Context, req RemovePersonRequestObject) (RemovePersonResponseObject, error) {
	bp, err := h.svc.RemovePerson(ctx, principal(ctx), req.Board, req.Handle)
	if err != nil {
		return nil, err
	}
	return convert[RemovePerson200JSONResponse](boardPersonOf(bp, req.Board))
}

// LeaveBoard takes the calling person off a board.
func (h *handlers) LeaveBoard(ctx context.Context, req LeaveBoardRequestObject) (LeaveBoardResponseObject, error) {
	bp, err := h.svc.Leave(ctx, principal(ctx), req.Board)
	if err != nil {
		return nil, err
	}
	return convert[LeaveBoard200JSONResponse](boardPersonOf(bp, req.Board))
}

// AddOwner makes a person on a board one of its owners.
func (h *handlers) AddOwner(ctx context.Context, req AddOwnerRequestObject) (AddOwnerResponseObject, error) {
	bp, _, err := h.svc.MakeOwner(ctx, principal(ctx), req.Board, req.Body.Handle)
	if err != nil {
		return nil, err
	}
	return convert[AddOwner200JSONResponse](boardPersonOf(bp, req.Board))
}

// SetVisibility turns a board open or private, or previews it.
func (h *handlers) SetVisibility(ctx context.Context, req SetVisibilityRequestObject) (SetVisibilityResponseObject, error) {
	dry := req.Body.DryRun != nil && *req.Body.DryRun
	c, err := h.svc.SetVisibility(ctx, principal(ctx), req.Board, string(req.Body.Visibility), dry)
	if err != nil {
		return nil, err
	}
	var reveals any
	if c.Reveals != nil {
		reveals = map[string]int64{"messages": c.Reveals.Messages, "files": c.Reveals.Files}
	}
	return convert[SetVisibility200JSONResponse](map[string]any{
		"board": c.Board, "before": c.Before, "after": c.After, "changed": c.Changed, "dry_run": c.DryRun,
		"reveals": reveals, "join_codes_canceled": c.CodesCanceled,
	})
}

// GetSettings returns the server's settings.
func (h *handlers) GetSettings(ctx context.Context, _ GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	st, err := h.svc.ServerSettings(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return GetSettings200JSONResponse{BoardCreation: BoardCreation(st.BoardCreation)}, nil
}

// UpdateSettings changes the server's settings, for an admin's own access key.
func (h *handlers) UpdateSettings(ctx context.Context, req UpdateSettingsRequestObject) (UpdateSettingsResponseObject, error) {
	var change board.Settings
	if req.Body.BoardCreation != nil {
		change.BoardCreation = string(*req.Body.BoardCreation)
	}
	st, err := h.svc.UpdateServerSettings(ctx, principal(ctx), change)
	if err != nil {
		return nil, err
	}
	return UpdateSettings200JSONResponse{BoardCreation: BoardCreation(st.BoardCreation)}, nil
}
