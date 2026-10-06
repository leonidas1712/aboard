package api

import "context"

func (h *handlers) CreateDelegatedBoard(context.Context, CreateDelegatedBoardRequestObject) (CreateDelegatedBoardResponseObject, error) {
	return nil, notImplemented("creating a board through a machine delegation")
}
