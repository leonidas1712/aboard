package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Approvals reads metadata only; collection is an explicit CLI operation.
type Approvals struct{ remote *Delegated }

// NewApprovals binds decision reads to one canonical issuer.
func NewApprovals(serverURL string, tokens Tokens) *Approvals {
	return &Approvals{remote: NewDelegated(serverURL, "approval", tokens)}
}

// Get uses the requesting seat and drops every secret outcome field.
func (a *Approvals) Get(ctx context.Context, id string, seat delivery.AgentRef) (delivery.ApprovalDecision, error) {
	var out delivery.ApprovalDecision
	if seat.Server != a.remote.url || seat.MemberID == "" {
		return out, delivery.ErrSeatMismatch
	}
	token, err := a.remote.tokens.AgentToken(seat)
	if err != nil {
		return out, err
	}
	status, raw, err := a.remote.sendKey(ctx, http.MethodGet, "/v1/me/approvals/"+url.PathEscape(id), token, nil, "")
	if err != nil {
		return out, err
	}
	if status < 200 || status >= 300 {
		return out, refusal(status, raw)
	}
	var wire struct {
		Approval struct {
			ID      string `json:"id"`
			AgentID string `json:"agent_id"`
			State   string `json:"state"`
		} `json:"approval"`
		PairingID string `json:"pairing_request_id"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return out, fmt.Errorf("read approval metadata: %w", err)
	}
	out = delivery.ApprovalDecision{ID: wire.Approval.ID, AgentID: wire.Approval.AgentID, State: wire.Approval.State, PairingRequestID: wire.PairingID}
	return out, nil
}
