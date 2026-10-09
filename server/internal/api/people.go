package api

import (
	"context"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// CreateServerInvite makes a server invite, for an admin's own access key.
func (h *handlers) CreateServerInvite(ctx context.Context, req CreateServerInviteRequestObject) (CreateServerInviteResponseObject, error) {
	if req.Body != nil && (req.Body.Boards != nil || req.Body.Pairing != nil) {
		return nil, notProvided("bundled onboarding invites")
	}
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
	if req.Body.ClientToken != nil {
		return nil, notProvided("client-generated onboarding keys")
	}
	in := board.ConnectInput{Invite: req.Body.Invite, Handle: req.Body.Handle, KeyName: req.Body.KeyName}
	if req.Body.DisplayName != nil {
		in.DisplayName = *req.Body.DisplayName
	}
	c, err := h.svc.Connect(ctx, in)
	if err != nil {
		return nil, err
	}
	key := map[string]any{
		"id": c.Key.ID, "name": c.Key.Name, "created_at": c.Key.CreatedAt, "expires_at": c.Key.ExpiresAt,
		"idle_expiry_seconds": c.Key.IdleSeconds, "token": c.Token,
	}
	return convert[Connect201JSONResponse](map[string]any{
		"server_id": h.svc.Config().ServerID, "person": personOf(c.Person), "key": key,
	})
}

// keyOf is an access key as the API shows it, without its secret.
func keyOf(k board.KeyView) map[string]any {
	return map[string]any{
		"id": k.ID, "name": k.Name, "created_at": k.CreatedAt, "expires_at": k.ExpiresAt,
		"idle_expiry_seconds": k.IdleSeconds, "state": k.State, "revoked_at": k.RevokedAt,
		"last_used_at": k.LastUsedAt, "browser_sessions": k.BrowserSessions, "agent_seats": k.AgentSeats,
		"delegations": k.Delegations,
	}
}

// ListKeys lists a person's access keys.
func (h *handlers) ListKeys(ctx context.Context, req ListKeysRequestObject) (ListKeysResponseObject, error) {
	handle := ""
	if req.Params.Person != nil {
		handle = *req.Params.Person
	}
	l, err := h.svc.ListKeys(ctx, principal(ctx), handle)
	if err != nil {
		return nil, err
	}
	keys := make([]map[string]any, 0, len(l.Keys))
	for _, k := range l.Keys {
		keys = append(keys, keyOf(k))
	}
	return convert[ListKeys200JSONResponse](map[string]any{
		"person": personOf(l.Person), "keys": keys,
		"current_key_id": l.CurrentKeyID, "current_key_previous_use": l.CurrentKeyPreviousUse,
	})
}

// CreateKey makes an access key for the caller and returns its secret, once.
func (h *handlers) CreateKey(ctx context.Context, req CreateKeyRequestObject) (CreateKeyResponseObject, error) {
	var ttl time.Duration
	if req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	k, err := h.svc.CreateKey(ctx, principal(ctx), req.Body.Name, ttl)
	if err != nil {
		return nil, err
	}
	out := keyOf(k.Key)
	out["token"] = k.Token
	return convert[CreateKey201JSONResponse](out)
}

// RevokeKey revokes an access key and everything it started.
func (h *handlers) RevokeKey(ctx context.Context, req RevokeKeyRequestObject) (RevokeKeyResponseObject, error) {
	k, err := h.svc.RevokeKey(ctx, principal(ctx), req.Key)
	if err != nil {
		return nil, err
	}
	return convert[RevokeKey200JSONResponse](keyOf(k))
}

// ListServerPeople lists the people on the server with their roles.
func (h *handlers) ListServerPeople(ctx context.Context, _ ListServerPeopleRequestObject) (ListServerPeopleResponseObject, error) {
	people, err := h.svc.ListServerPeople(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(people))
	for _, p := range people {
		out = append(out, personOf(p))
	}
	return convert[ListServerPeople200JSONResponse](map[string]any{"people": out})
}

// SetServerRole makes a person an admin of the server, or a member again.
func (h *handlers) SetServerRole(ctx context.Context, req SetServerRoleRequestObject) (SetServerRoleResponseObject, error) {
	c, err := h.svc.SetServerRole(ctx, principal(ctx), req.Handle, string(req.Body.ServerRole))
	if err != nil {
		return nil, err
	}
	return convert[SetServerRole200JSONResponse](map[string]any{"person": personOf(c.Person), "changed": c.Changed})
}

// RemoveFromServer removes a person from the server, or previews it.
func (h *handlers) RemoveFromServer(ctx context.Context, req RemoveFromServerRequestObject) (RemoveFromServerResponseObject, error) {
	dry := req.Params.DryRun != nil && *req.Params.DryRun
	r, err := h.svc.RemoveFromServer(ctx, principal(ctx), req.Handle, dry)
	if err != nil {
		return nil, err
	}
	return convert[RemoveFromServer200JSONResponse](map[string]any{
		"person": personOf(r.Person), "dry_run": r.DryRun, "keys_revoked": r.KeysRevoked,
		"browser_sessions_ended": r.BrowserSessions, "agents_removed": r.Agents, "boards_left": r.Boards,
		"owners_passed": r.OwnersPassed, "unreachable_boards": r.Unreachable,
	})
}

// personOf is a person as the API shows them.
func personOf(p board.Human) map[string]any {
	return map[string]any{"id": p.ID, "handle": p.Name, "display_name": p.DisplayName, "server_role": p.Role, "created_at": p.CreatedAt}
}
