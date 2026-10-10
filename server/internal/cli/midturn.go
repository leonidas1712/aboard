package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

type midturnOutput struct {
	Server  serverRef                   `json:"server"`
	Board   string                      `json:"board,omitempty"`
	Agent   string                      `json:"agent,omitempty"`
	Policy  api.MidturnPolicy           `json:"policy"`
	Source  api.MidturnPolicyViewSource `json:"source"`
	Changed bool                        `json:"changed"`
}

func runMidturnDelivery(ctx context.Context, a *app, args []string) error {
	const use = "aboard delivery midturn [owner-only|my-agents] [--as AGENT] [--board BOARD] [--inherit] [--server SERVER]"
	fs := a.flags("delivery midturn")
	as := fs.String("as", "", "your agent whose override to read or change")
	boardFlag := fs.String("board", "", "the selected agent's board")
	serverFlag := fs.String("server", "", "issuer server name or URL")
	inherit := fs.Bool("inherit", false, "clear your selected agent's override")
	pos, err := a.parse(fs, args, use, 0, 1)
	if err != nil {
		return err
	}
	changing := len(pos) != 0 || *inherit
	if changing && a.actsForAgent() {
		return a.refuseInSession("Changing mid-turn delivery", "aboard delivery midturn owner-only")
	}
	if *inherit && (len(pos) != 0 || *as == "") {
		return usageError("--inherit clears a selected agent override; use --as without a policy.", use)
	}
	if len(pos) != 0 && pos[0] != "owner-only" && pos[0] != "my-agents" {
		return usageError("Use owner-only or my-agents.", use)
	}
	var t target
	var cred agentCredential
	var c *client
	memberID := ""
	switch {
	case a.actsForAgent():
		t, cred, err = a.agentTarget(ctx, *boardFlag, *as)
		if err != nil {
			return err
		}
		if *serverFlag != "" {
			srv, err := a.resolveServer(*serverFlag)
			if err != nil {
				return err
			}
			if srv.URL != t.server.URL {
				return newError("session_on_another_server", "The selected agent belongs to another server.", "Use a session for that server.")
			}
		}
		c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
	case *as != "":
		if *serverFlag != "" {
			t, err = a.humanBoard(ctx, *boardFlag)
			if err != nil {
				return err
			}
			t.server, err = a.resolveServer(*serverFlag)
			if err != nil {
				return err
			}
			if t.board == "" {
				return usageError("Name the agent's board with --board.", use)
			}
			cred = agentCredential{Server: t.server.URL, Board: t.board, Name: strings.TrimPrefix(strings.TrimSpace(*as), "@")}
		} else {
			t, cred, _, err = a.deliveryTarget(ctx, *boardFlag, *as)
			if err != nil {
				return err
			}
		}
		c, err = a.humanClient(ctx, t)
		if err == nil {
			rctx, cancel := context.WithTimeout(ctx, requestTimeout)
			me, identityErr := c.api.GetMeWithResponse(rctx)
			cancel()
			if identityErr != nil {
				cancel()
				return c.unreachable(identityErr)
			}
			if me.JSON200 == nil {
				cancel()
				return apiError(me.StatusCode(), me.Body)
			}
			rctx, cancel = context.WithTimeout(ctx, requestTimeout)
			members, fetchErr := c.api.ListMembersWithResponse(rctx, t.board, nil)
			cancel()
			if fetchErr != nil {
				return c.unreachable(fetchErr)
			}
			if members.JSON200 == nil {
				return apiError(members.StatusCode(), members.Body)
			}
			for _, m := range members.JSON200.Members {
				if m.Kind == "agent" && m.Name == cred.Name {
					if m.OwnerId == nil || *m.OwnerId != me.JSON200.Id {
						return newError("agent_owner_required", "Only the agent's own person may read or change its override.", "Ask its person to run aboard delivery midturn.")
					}
					memberID = m.Id
					break
				}
			}
			if memberID == "" {
				return newError("member_not_found", "That active agent is not available.", "Run aboard board people on the selected board.")
			}
		}
	default:
		t.server, err = a.resolveServer(*serverFlag)
		if err == nil {
			c, err = a.humanClient(ctx, t)
		}
	}
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var view *api.MidturnPolicyView
	if changing {
		input := api.SetMidturnPolicyJSONRequestBody{}
		if memberID != "" {
			input.MemberId = &memberID
		}
		if !*inherit {
			policy := api.MidturnPolicy(pos[0])
			input.Policy = &policy
		}
		r, err := c.api.SetMidturnPolicyWithResponse(rctx, nil, input)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		view = r.JSON200
	} else {
		r, err := c.api.GetMidturnPolicyWithResponse(rctx)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		view = r.JSON200
	}
	if memberID != "" {
		// The person's response names its default; apply only the selected seat's override.
		if view.Overrides != nil {
			for _, override := range *view.Overrides {
				if override.MemberId == memberID {
					view.Policy, view.Source = override.Policy, "agent_override"
					break
				}
			}
		}
		if changing && !*inherit {
			view.Policy, view.Source = api.MidturnPolicy(pos[0]), "agent_override"
		}
	}
	out := midturnOutput{Server: t.server, Policy: view.Policy, Source: view.Source}
	if *as != "" || a.actsForAgent() {
		out.Board, out.Agent = cred.Board, cred.Name
	}
	if view.Changed != nil {
		out.Changed = *view.Changed
	}
	who := t.server.Name
	if out.Agent != "" {
		who = out.Agent + " on " + out.Board
	}
	a.emit(out, fmt.Sprintf("%s: mid-turn %s (%s)\n", who, out.Policy, out.Source))
	return nil
}
