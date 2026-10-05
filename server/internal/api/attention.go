package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// unreadEvent is the data of one `unread` event, the UnreadEvent schema.
type unreadEvent struct {
	Board    string `json:"board"`
	BoardID  string `json:"board_id"`
	ReadUpTo int64  `json:"read_up_to"`
	Unread   int64  `json:"unread"`
}

func (h *handlers) AckBoard(ctx context.Context, req AckBoardRequestObject) (AckBoardResponseObject, error) {
	pos, err := h.svc.AckBoard(ctx, principal(ctx), req.Board, int64(req.Body.UpTo))
	if err != nil {
		return nil, err
	}
	return convert[AckBoard200JSONResponse](map[string]any{
		"board": pos.Board, "read_up_to": pos.Position.ReadUpTo, "unread": pos.Position.Unread,
	})
}

type wireReceipt struct {
	Member   wireMemberRef `json:"member"`
	State    string        `json:"state"`
	Presence *string       `json:"presence"`
}

func (h *handlers) GetReceipts(ctx context.Context, req GetReceiptsRequestObject) (GetReceiptsResponseObject, error) {
	r, err := h.svc.Receipts(ctx, principal(ctx), req.Board, int64(req.Seq))
	if err != nil {
		return nil, err
	}
	recipients := make([]wireReceipt, 0, len(r.Recipients))
	for _, rc := range r.Recipients {
		w := wireReceipt{Member: refOf(rc.Member), State: rc.State}
		if rc.Presence != nil {
			w.Presence = &rc.Presence.State
		}
		recipients = append(recipients, w)
	}
	return convert[GetReceipts200JSONResponse](struct {
		Board      string        `json:"board"`
		Seq        int64         `json:"seq"`
		MessageID  string        `json:"message_id"`
		To         []string      `json:"to"`
		ToEveryone bool          `json:"to_everyone"`
		Available  bool          `json:"available"`
		Recipients []wireReceipt `json:"recipients"`
	}{r.Board.Name, r.Message.Seq, r.Message.ID, r.Message.To, r.ToEveryone, r.Available, recipients})
}

// withPosition adds the caller's read position to a board's wire form, when the read
// reported one.
func withPosition(w wireBoard, v board.View) wireBoard {
	if v.Position != nil {
		w.ReadUpTo, w.Unread = &v.Position.ReadUpTo, &v.Position.Unread
	}
	return w
}
