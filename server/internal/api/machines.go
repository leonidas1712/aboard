package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

type clientAddrKey struct{}

// clientAddr is the address the request came from, as the server saw it.
func clientAddr(ctx context.Context) string {
	a, _ := ctx.Value(clientAddrKey{}).(string)
	return a
}

// StartMachineRequest records a new machine's request for a key. It needs no token.
func (h *handlers) StartMachineRequest(ctx context.Context, req StartMachineRequestRequestObject) (StartMachineRequestResponseObject, error) {
	r, err := h.svc.StartMachineRequest(ctx, req.Body.Handle, req.Body.Label, clientAddr(ctx))
	if err != nil {
		return nil, err
	}
	return convert[StartMachineRequest201JSONResponse](map[string]any{
		"code": r.Code, "secret": r.Secret, "expires_at": r.ExpiresAt, "poll_interval_seconds": int(r.PollEvery.Seconds()),
	})
}

// machineRequestOf is a machine request as the person deciding it sees it.
func machineRequestOf(v board.MachineRequestView) map[string]any {
	return map[string]any{
		"label": v.Request.Label, "state": v.Request.State, "requested_from": v.Request.RequestedFrom,
		"created_at": v.Request.CreatedAt, "expires_at": v.Request.ExpiresAt, "person": personOf(v.Person),
	}
}

// LookupMachineRequest shows the pending request a short code names.
func (h *handlers) LookupMachineRequest(ctx context.Context, req LookupMachineRequestRequestObject) (LookupMachineRequestResponseObject, error) {
	v, err := h.svc.LookupMachineRequest(ctx, principal(ctx), req.Body.Code)
	if err != nil {
		return nil, err
	}
	return convert[LookupMachineRequest200JSONResponse](machineRequestOf(v))
}

// ApproveMachineRequest approves a machine request for the caller.
func (h *handlers) ApproveMachineRequest(ctx context.Context, req ApproveMachineRequestRequestObject) (ApproveMachineRequestResponseObject, error) {
	v, err := h.svc.ApproveMachineRequest(ctx, principal(ctx), req.Body.Code)
	if err != nil {
		return nil, err
	}
	return convert[ApproveMachineRequest200JSONResponse](machineRequestOf(v))
}

// RefuseMachineRequest refuses a machine request.
func (h *handlers) RefuseMachineRequest(ctx context.Context, req RefuseMachineRequestRequestObject) (RefuseMachineRequestResponseObject, error) {
	v, err := h.svc.RefuseMachineRequest(ctx, principal(ctx), req.Body.Code)
	if err != nil {
		return nil, err
	}
	return convert[RefuseMachineRequest200JSONResponse](machineRequestOf(v))
}

// CollectMachineRequest answers a waiting machine: still pending, or its new key, once.
// It needs no token: the collection secret is the proof.
func (h *handlers) CollectMachineRequest(ctx context.Context, req CollectMachineRequestRequestObject) (CollectMachineRequestResponseObject, error) {
	c, err := h.svc.CollectMachineRequest(ctx, req.Body.Secret)
	if err != nil {
		return nil, err
	}
	if c.Pending {
		return convert[CollectMachineRequest202JSONResponse](map[string]any{
			"state": board.MachinePending, "expires_at": c.ExpiresAt, "poll_interval_seconds": int(c.PollEvery.Seconds()),
		})
	}
	k := c.Connected.Key
	key := map[string]any{
		"id": k.ID, "name": k.Name, "created_at": k.CreatedAt, "expires_at": k.ExpiresAt,
		"idle_expiry_seconds": k.IdleSeconds, "token": c.Connected.Token,
	}
	return convert[CollectMachineRequest201JSONResponse](map[string]any{
		"server_id": h.svc.Config().ServerID, "person": personOf(c.Connected.Person), "key": key,
	})
}
