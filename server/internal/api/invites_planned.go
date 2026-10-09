package api

import "context"

func (h *handlers) ListServerInvites(context.Context, ListServerInvitesRequestObject) (ListServerInvitesResponseObject, error) {
	return nil, notProvided("invitation metadata")
}

func (h *handlers) RevokeServerInvite(context.Context, RevokeServerInviteRequestObject) (RevokeServerInviteResponseObject, error) {
	return nil, notProvided("invitation revocation")
}
