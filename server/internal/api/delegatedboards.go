package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

type requestHashKey struct{}

// CreateDelegatedBoard keeps the person as creator while giving the session a seat.
func (h *handlers) CreateDelegatedBoard(ctx context.Context, req CreateDelegatedBoardRequestObject) (CreateDelegatedBoardResponseObject, error) {
	in, err := convert[struct {
		Name, Title, Template, Charter, Preset, Visibility string
		Session, Harness, Role                             string
		AgentName                                          string `json:"agent_name"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	hash, _ := ctx.Value(requestHashKey{}).(string)
	joined, replayed, err := h.svc.CreateDelegatedBoard(ctx, principal(ctx), board.DelegatedBoard{NewBoard: board.NewBoard{Name: in.Name, Title: in.Title, Template: in.Template, Charter: in.Charter, Preset: in.Preset, Visibility: in.Visibility}, Session: in.Session, Harness: in.Harness, Role: in.Role, AgentName: in.AgentName, Key: req.Params.IdempotencyKey, RequestHash: hash})
	if err != nil {
		return nil, err
	}
	body, err := convert[JoinResult](joinedOf(joined, board.Principal{Agent: &joined.Agent}))
	if err != nil {
		return nil, err
	}
	noStore := "no-store"
	headers := CreateDelegatedBoard201ResponseHeaders{CacheControl: &noStore}
	if replayed {
		headers.IdempotentReplayed = &replayed
	}
	return CreateDelegatedBoard201JSONResponse{Body: body, Headers: headers}, nil
}
