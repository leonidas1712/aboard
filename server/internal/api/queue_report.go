package api

import (
	"context"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func queueWire(q board.QueueReport) DeliveryQueueView {
	out := DeliveryQueueView{BoardId: q.BoardID, MemberId: q.MemberID, Epoch: q.Epoch, Revision: q.Revision}
	if at, err := time.Parse(time.RFC3339Nano, q.ExpiresAt); err == nil {
		out.ExpiresAt = &at
	}
	return out
}

func (h *handlers) GetDeliveryQueue(ctx context.Context, _ GetDeliveryQueueRequestObject) (GetDeliveryQueueResponseObject, error) {
	q, err := h.svc.DeliveryQueue(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return GetDeliveryQueue200JSONResponse(queueWire(q)), nil
}

func (h *handlers) ReportDeliveryQueue(ctx context.Context, req ReportDeliveryQueueRequestObject) (ReportDeliveryQueueResponseObject, error) {
	in := board.QueueInput{Session: req.Body.Session, Boot: req.Body.Boot, ExpectedEpoch: req.Body.ExpectedEpoch, Epoch: req.Body.Epoch, Revision: req.Body.Revision}
	if req.Body.Messages != nil {
		ids := make([]board.QueueIdentity, 0, len(*req.Body.Messages))
		for _, id := range *req.Body.Messages {
			ids = append(ids, board.QueueIdentity{MessageID: id.MessageId, Seq: int64(id.Seq)})
		}
		in.Messages = &ids
	}
	q, err := h.svc.ReportDeliveryQueue(ctx, principal(ctx), in)
	if err != nil {
		return nil, err
	}
	return ReportDeliveryQueue200JSONResponse(queueWire(q)), nil
}
