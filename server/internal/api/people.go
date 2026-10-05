package api

import (
	"context"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// CreateServerInvite makes a server invite, for an admin's own access key.
func (h *handlers) CreateServerInvite(ctx context.Context, req CreateServerInviteRequestObject) (CreateServerInviteResponseObject, error) {
	var ttl time.Duration
	if req.Body != nil && req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	inv, err := h.svc.CreateServerInvite(ctx, principal(ctx), ttl)
	if err != nil {
		return nil, err
	}
	return convert[CreateServerInvite201JSONResponse](map[string]string{
		"id": inv.Invite.ID, "invite": inv.Secret, "server_role": board.ServerMember, "expires_at": inv.Invite.ExpiresAt,
	})
}

// Connect redeems a server invite. It needs no token: the invite is the proof.
func (h *handlers) Connect(ctx context.Context, req ConnectRequestObject) (ConnectResponseObject, error) {
	in := board.ConnectInput{Invite: req.Body.Invite, Handle: req.Body.Handle, KeyName: req.Body.KeyName}
	if req.Body.DisplayName != nil {
		in.DisplayName = *req.Body.DisplayName
	}
	c, err := h.svc.Connect(ctx, in)
	if err != nil {
		return nil, err
	}
	key := map[string]any{"id": c.Key.ID, "name": c.Key.Name, "created_at": c.Key.CreatedAt, "expires_at": c.Key.ExpiresAt, "token": c.Token}
	return convert[Connect201JSONResponse](map[string]any{
		"server_id": h.svc.Config().ServerID, "person": personOf(c.Person), "key": key,
	})
}

// personOf is a person as the API shows them.
func personOf(p board.Human) map[string]any {
	return map[string]any{"id": p.ID, "handle": p.Name, "display_name": p.DisplayName, "server_role": p.Role, "created_at": p.CreatedAt}
}
