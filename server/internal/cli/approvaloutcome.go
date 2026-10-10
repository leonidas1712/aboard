package cli

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func (a *app) showApproval(ctx context.Context, c *client, srv serverRef, board, id string, collect bool) error {
	var outcome *api.ApprovalOutcome
	if collect {
		r, err := c.api.CollectApprovalWithResponse(ctx, id, nil)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		outcome = r.JSON200
	} else {
		r, err := c.api.GetApprovalWithResponse(ctx, id)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		outcome = r.JSON200
	}
	out := map[string]any{"server": srv, "approval": outcome.Approval, "collected": outcome.Collected}
	text := fmt.Sprintf("%s · %s on %s", outcome.Approval.Id, outcome.Approval.State, srv.Name)
	if board != "" {
		out["board"] = board
		text += " · " + board
	}
	text += "\n"
	if outcome.PairingRequestId != nil {
		out["pairing_request_id"] = *outcome.PairingRequestId
	}
	if outcome.Invite != nil {
		out["invite"] = outcome.Invite
		link := srv.URL + "/join#" + outcome.Invite.Invite
		prompt := serverInvitePrompt(link, outcome.Invite.PairingRequestId != nil, deref(outcome.Invite.SuggestedHandle))
		out["prompt"] = prompt
		text += "Invite: " + link + "\n" + prompt + "\n"
	}
	if outcome.Next != nil {
		out["next"] = outcome.Next
		text += outcome.Next.Command + "\n" + outcome.Next.Resume + "\n"
	}
	a.emit(out, text)
	return nil
}
