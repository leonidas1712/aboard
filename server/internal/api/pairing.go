package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func pairingEndpointOf(e *board.PairingEndpoint) any {
	if e == nil {
		return nil
	}
	return map[string]any{"person_id": e.PersonID, "agent_id": e.AgentID, "session_binding": e.SessionBinding, "generation": e.Generation}
}

func pairingOf(r board.PairingRequest) map[string]any {
	out := map[string]any{"id": r.ID, "server_id": r.ServerID, "board_id": r.BoardID, "inviter_id": r.InviterID, "initiating_agent_id": r.InitiatingAgentID, "work": r.Work, "state": r.State, "generation": r.Generation, "created_at": r.CreatedAt, "expires_at": r.ExpiresAt}
	if r.RecipientID != "" {
		out["recipient_id"] = r.RecipientID
	}
	if r.InviteID != "" {
		out["invite_id"] = r.InviteID
	}
	if r.Initiator != nil {
		out["initiator"] = pairingEndpointOf(r.Initiator)
	}
	if r.Recipient != nil {
		out["recipient"] = pairingEndpointOf(r.Recipient)
	}
	if r.State == "verifying" {
		awaiting := "both"
		if r.Forward != nil {
			awaiting = "recipient"
		}
		if r.Reverse != nil {
			awaiting = "initiator"
		}
		out["awaiting"] = awaiting
	}
	return out
}

func (h *handlers) ListPairingRequests(ctx context.Context, _ ListPairingRequestsRequestObject) (ListPairingRequestsResponseObject, error) {
	rs, err := h.svc.ListPairings(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rs {
		out = append(out, pairingOf(r))
	}
	return convert[ListPairingRequests200JSONResponse](map[string]any{"requests": out})
}

func (h *handlers) CreatePairingRequest(ctx context.Context, req CreatePairingRequestRequestObject) (CreatePairingRequestResponseObject, error) {
	in := req.Body
	r, err := h.svc.CreatePairing(ctx, principal(ctx), in.BoardId, in.RecipientId, in.InitiatingAgentId, in.Work)
	if err != nil {
		return nil, err
	}
	return convert[CreatePairingRequest201JSONResponse](pairingOf(r))
}

func (h *handlers) GetPairingRequest(ctx context.Context, req GetPairingRequestRequestObject) (GetPairingRequestResponseObject, error) {
	r, err := h.svc.GetPairing(ctx, principal(ctx), req.Pairing)
	if err != nil {
		return nil, err
	}
	return convert[GetPairingRequest200JSONResponse](pairingOf(r))
}

func (h *handlers) DeclinePairingRequest(ctx context.Context, req DeclinePairingRequestRequestObject) (DeclinePairingRequestResponseObject, error) {
	r, err := h.svc.ClosePairing(ctx, principal(ctx), req.Pairing, "declined")
	if err != nil {
		return nil, err
	}
	return convert[DeclinePairingRequest200JSONResponse](pairingOf(r))
}

func (h *handlers) CancelPairingRequest(ctx context.Context, req CancelPairingRequestRequestObject) (CancelPairingRequestResponseObject, error) {
	r, err := h.svc.ClosePairing(ctx, principal(ctx), req.Pairing, "cancelled")
	if err != nil {
		return nil, err
	}
	return convert[CancelPairingRequest200JSONResponse](pairingOf(r))
}

func (h *handlers) CreatePairingCredential(ctx context.Context, req CreatePairingCredentialRequestObject) (CreatePairingCredentialResponseObject, error) {
	in := req.Body
	token := ""
	if in.ClientToken != nil {
		token = *in.ClientToken
	}
	c, r, err := h.svc.MintPairingCredential(ctx, principal(ctx), board.PairingSelection{RequestID: in.RequestId, Side: string(in.Side), AgentID: in.AgentId, SessionBinding: in.SessionBinding, ClientToken: token, Generation: in.Generation, Replace: in.Replace != nil && *in.Replace})
	if err != nil {
		return nil, err
	}
	return convert[CreatePairingCredential200JSONResponse](map[string]any{"id": c.ID, "request": pairingOf(r), "side": c.Side, "endpoint": pairingEndpointOf(&c.Endpoint), "expires_at": c.ExpiresAt})
}

func (h *handlers) AcceptPairingRequest(ctx context.Context, req AcceptPairingRequestRequestObject) (AcceptPairingRequestResponseObject, error) {
	r, err := h.svc.AcceptPairing(ctx, principal(ctx), req.Pairing, req.Body.AgentId, req.Body.Generation)
	if err != nil {
		return nil, err
	}
	return convert[AcceptPairingRequest200JSONResponse](pairingOf(r))
}

func (h *handlers) VerifyPairingRoundTrip(ctx context.Context, req VerifyPairingRoundTripRequestObject) (VerifyPairingRoundTripResponseObject, error) {
	in := req.Body
	r, err := h.svc.VerifyPairing(ctx, principal(ctx), req.Pairing, string(in.Direction), in.Generation, int64(in.PingSeq), int64(in.ReplySeq), in.HandoffId)
	if err != nil {
		return nil, err
	}
	return convert[VerifyPairingRoundTrip200JSONResponse](pairingOf(r))
}
