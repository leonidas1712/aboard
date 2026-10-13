package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (h *handlers) ListRepliesWaiting(ctx context.Context, req ListRepliesWaitingRequestObject) (ListRepliesWaitingResponseObject, error) {
	f := board.RepliesWaitingFilter{Limit: limitOr(req.Params.Limit)}
	if req.Params.Board != nil {
		f.Board = *req.Params.Board
	}
	listing, err := h.svc.RepliesWaiting(ctx, principal(ctx), f)
	if err != nil {
		return nil, err
	}
	msgs := []wireMessage{}
	for _, request := range listing.Requests {
		msgs = append(msgs, messagesOf(board.Reading{Board: request.Board, Reader: request.Reader, Messages: []board.Message{request.Message}})...)
	}
	return convert[ListRepliesWaiting200JSONResponse](map[string]any{"messages": msgs, "more": listing.More, "count": listing.Count})
}
