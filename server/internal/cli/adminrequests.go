package cli

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func (a *app) requestServerRole(ctx context.Context, server, handle, role string) error {
	srv, board, c, err := a.admissionClient(ctx, server, "", "")
	if err != nil {
		return err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	lookup, err := c.api.ListServerPeopleWithResponse(ctx, &api.ListServerPeopleParams{Handle: &handle})
	if err != nil {
		return c.unreachable(err)
	}
	if lookup.JSON200 == nil {
		return apiError(lookup.StatusCode(), lookup.Body)
	}
	person, err := lookup.JSON200.AsPersonIdentityLookup()
	if err != nil {
		return err
	}
	if person.Id == "" {
		return newError("internal", "The server did not return the person's identity.", "Check that the server supports exact person lookup.")
	}
	var action api.AdminAction
	if err := action.FromSetServerRoleAction(api.SetServerRoleAction{Kind: api.SetServerRole, PersonId: person.Id, Role: api.SetServerRoleActionRole(role)}); err != nil {
		return err
	}
	return requestAdmission(ctx, a, c, srv, board, action)
}

func (a *app) requestBoardPolicy(ctx context.Context, board string, preset api.PolicyPreset) error {
	srv, selected, c, err := a.admissionClient(ctx, a.boardServerFlag, board, "")
	if err != nil {
		return err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	current, err := c.board(ctx, selected)
	if err != nil {
		return err
	}
	var action api.AdminAction
	if err := action.FromSetBoardPolicyAction(api.SetBoardPolicyAction{Kind: api.SetBoardPolicy, BoardId: current.Id, Policy: api.PolicyChange{Preset: &preset}}); err != nil {
		return err
	}
	return requestAdmission(ctx, a, c, srv, selected, action)
}
