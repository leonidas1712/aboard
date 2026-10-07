package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func askOf(a *board.Ask) any {
	if a == nil {
		return nil
	}
	return map[string]any{"to": refOf(a.Target), "options": a.Options, "blocking": a.Blocking, "going_with": a.GoingWith, "going_at": a.GoingAt, "task": taskRefOf(a.Task), "state": a.State, "answer_seq": a.AnswerSeq, "answer_option": a.AnswerOption}
}

func answerOf(a *board.Answer) any {
	if a == nil {
		return nil
	}
	return map[string]any{"ask_id": a.AskID, "ask_seq": a.AskSeq, "option": a.Option, "option_text": a.OptionText, "withdrawn": a.Withdrawn}
}

func blockedOf(bs []board.BlockedOn) []any {
	out := []any{}
	for _, b := range bs {
		out = append(out, map[string]any{"ask_id": b.AskID, "ask_seq": b.AskSeq, "to": refOf(b.To), "since": b.Since})
	}
	return out
}

func (h *handlers) ListAsks(ctx context.Context, req ListAsksRequestObject) (ListAsksResponseObject, error) {
	f := board.AskFilter{Limit: limitOr(req.Params.Limit)}
	if req.Params.Board != nil {
		f.Board = *req.Params.Board
	}
	if req.Params.Task != nil {
		f.Task = *req.Params.Task
	}
	if req.Params.State != nil {
		f.State = string(*req.Params.State)
	}
	if req.Params.ToMe != nil {
		f.ToMe = *req.Params.ToMe
	}
	if req.Params.FromMe != nil {
		f.FromMe = *req.Params.FromMe
	}
	v, e := h.svc.ListAsks(ctx, principal(ctx), f)
	if e != nil {
		return nil, e
	}
	out := []wireMessage{}
	for _, a := range v.Asks {
		r := board.Reading{Board: a.Board, Reader: a.Reader, Messages: []board.Message{a.Message}}
		out = append(out, messagesOf(r)...)
	}
	return convert[ListAsks200JSONResponse](map[string]any{"asks": out, "more": v.More})
}
