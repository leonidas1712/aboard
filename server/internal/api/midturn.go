package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func midturnWire(v board.MidturnView) MidturnPolicyView {
	out := MidturnPolicyView{Policy: MidturnPolicy(v.Policy), Source: MidturnPolicyViewSource(v.Source)}
	if v.Overrides != nil {
		overrides := make([]struct {
			MemberId string        `json:"member_id"` //nolint:revive // matches the generated anonymous response type
			Policy   MidturnPolicy `json:"policy"`
		}, 0, len(v.Overrides))
		for _, o := range v.Overrides {
			overrides = append(overrides, struct {
				MemberId string        `json:"member_id"` //nolint:revive // matches the generated anonymous response type
				Policy   MidturnPolicy `json:"policy"`
			}{MemberId: o.MemberID, Policy: MidturnPolicy(o.Policy)})
		}
		out.Overrides = &overrides
	}
	return out
}

func (h *handlers) GetMidturnPolicy(ctx context.Context, _ GetMidturnPolicyRequestObject) (GetMidturnPolicyResponseObject, error) {
	view, err := h.svc.MidturnPolicy(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	return GetMidturnPolicy200JSONResponse(midturnWire(view)), nil
}

func (h *handlers) SetMidturnPolicy(ctx context.Context, req SetMidturnPolicyRequestObject) (SetMidturnPolicyResponseObject, error) {
	memberID := ""
	if req.Body.MemberId != nil {
		memberID = *req.Body.MemberId
	}
	var policy *string
	if req.Body.Policy != nil {
		p := string(*req.Body.Policy)
		policy = &p
	}
	view, err := h.svc.SetMidturnPolicy(ctx, principal(ctx), memberID, policy)
	if err != nil {
		return nil, err
	}
	// Override lists are current reads, not retained write receipts.
	view.Overrides = nil
	out := midturnWire(view)
	out.Changed = &view.Changed
	return SetMidturnPolicy200JSONResponse(out), nil
}
