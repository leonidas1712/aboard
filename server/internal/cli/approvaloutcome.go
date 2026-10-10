package cli

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func (a *app) showApproval(ctx context.Context, c *client, srv serverRef, board, id string, collect bool) error {
	var outcome *api.ApprovalOutcome
	if collect {
		key, err := idempotencyKey(a.env.Rand)
		if err != nil {
			return err
		}
		params := &api.CollectApprovalParams{IdempotencyKey: &key}
		r, err := c.api.CollectApprovalWithResponse(ctx, id, params)
		if err != nil && ctx.Err() == nil {
			r, err = c.api.CollectApprovalWithResponse(ctx, id, params)
		}
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
	if collect && outcome.Invite == nil && outcome.Approval.Execution != nil && outcome.Approval.Execution.InviteId != nil {
		server := " --server " + commandWord(srv.Name)
		request := "aboard invite --person" + server
		if board != "" {
			request += " --board " + commandWord(board)
		}
		outcome.Next = &api.NextStep{
			Command: "aboard invite revoke " + commandWord(*outcome.Approval.Execution.InviteId) + server,
			Resume:  "Already collected, revoked or expired. If you lost the link, ask your person to revoke it with this command, then request a new invite: " + request,
		}
	}
	if outcome.Next != nil {
		outcome.Next.Command = labelOnboardingCommand(outcome.Next.Command, srv)
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
		link, prompt := inviteHandover(srv, *outcome.Invite)
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

func inviteHandover(srv serverRef, invite api.ServerInvite) (link, prompt string) {
	link = deref(invite.Link)
	if link == "" {
		link = srv.URL + "/join#" + invite.Invite
	}
	prompt = deref(invite.Prompt)
	if prompt == "" {
		prompt = serverInvitePrompt(link, invite.PairingRequestId != nil, deref(invite.SuggestedHandle))
	}
	return link, prompt
}
