package api

import "context"

func (h *handlers) ArchiveBoard(ctx context.Context, req ArchiveBoardRequestObject) (ArchiveBoardResponseObject, error) {
	selector, err := convert[string](req.Board)
	if err != nil {
		return nil, err
	}
	result, err := h.svc.ArchiveBoard(ctx, principal(ctx), selector)
	if err != nil {
		return nil, err
	}
	return ArchiveBoard200JSONResponse{Id: result.ID, Lifecycle: LifecycleState(result.Lifecycle), Changed: result.Changed}, nil
}

func (h *handlers) RestoreBoard(ctx context.Context, req RestoreBoardRequestObject) (RestoreBoardResponseObject, error) {
	selector, err := convert[string](req.Board)
	if err != nil {
		return nil, err
	}
	result, err := h.svc.RestoreBoard(ctx, principal(ctx), selector)
	if err != nil {
		return nil, err
	}
	return RestoreBoard200JSONResponse{Id: result.ID, Lifecycle: LifecycleState(result.Lifecycle), Changed: result.Changed}, nil
}

func (h *handlers) DeleteBoard(ctx context.Context, req DeleteBoardRequestObject) (DeleteBoardResponseObject, error) {
	selector, err := convert[string](req.Board)
	if err != nil {
		return nil, err
	}
	result, err := h.svc.DeleteBoard(ctx, principal(ctx), selector)
	if err != nil {
		return nil, err
	}
	return DeleteBoard200JSONResponse{Id: result.ID, Lifecycle: LifecycleState(result.Lifecycle), Changed: result.Changed}, nil
}
