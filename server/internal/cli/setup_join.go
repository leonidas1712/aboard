package cli

import (
	"context"
	"encoding/json"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// joinSetupBoards takes board IDs only from the authenticated, freshly read receipt.
// The daemon vouches for the current session and keeps all of its other issuer seats.
func (a *app) joinSetupBoards(ctx context.Context, out *setupOutput, boards []string) (bool, error) {
	if len(boards) == 0 {
		out.Steps[4].State = "complete"
		return true, nil
	}
	key, known, err := a.checkSession(ctx)
	if err != nil {
		return false, err
	}
	if !known {
		out.Steps[4].Message = "Continue Aboard setup in the harness session that should join these boards."
		return false, nil
	}
	bound, err := a.currentSetupBoards(ctx, key, out.Server)
	if err != nil {
		return false, err
	}
	for _, id := range boards {
		if bound[id] {
			continue
		}
		resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpJoin, Harness: key.Harness, Session: key.ID, Agent: &delivery.AgentRef{Server: out.Server.URL, Board: id}})
		if err != nil {
			return false, err
		}
		var board api.Board
		if resp.Joined == nil || json.Unmarshal(resp.Board, &board) != nil || board.Id != id || resp.Joined.Server != out.Server.URL || resp.Joined.MemberID == "" {
			return false, newError("internal", "The joined board did not match the authenticated setup receipt.", "Continue Aboard setup; do not make a replacement account.")
		}
	}
	out.Steps[4].State = "complete"
	out.Steps[4].Message = "This exact session joined the invited boards; existing seats were reused."
	return true, nil
}

// Current bindings are checked with their own credentials before being retained.
// Reading them never rotates a token or replaces a handshake's endpoint generation.
func (a *app) currentSetupBoards(ctx context.Context, key delivery.SessionKey, srv serverRef) (map[string]bool, error) {
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return nil, err
	}
	selected := []delivery.AgentRef{}
	for _, agent := range agents {
		if agent.Server == srv.URL && agent.MemberID != "" {
			selected = append(selected, agent)
		}
	}
	bound := map[string]bool{}
	if len(selected) == 0 {
		return bound, nil
	}
	creds, err := a.readCredentials()
	if err != nil {
		return nil, err
	}
	for _, agent := range selected {
		cred, ok := creds.forSeat(agent)
		if !ok {
			continue
		}
		c, err := a.client(ctx, srv, cred.Token, requestTimeout)
		if err != nil {
			return nil, err
		}
		me, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return nil, c.unreachable(err)
		}
		if me.JSON200 == nil || me.JSON200.Id != agent.MemberID || me.JSON200.Board == nil {
			continue
		}
		board, err := c.api.GetBoardWithResponse(ctx, *me.JSON200.Board)
		if err != nil {
			return nil, c.unreachable(err)
		}
		if board.JSON200 == nil || (board.JSON200.Lifecycle != nil && *board.JSON200.Lifecycle != api.BoardLifecycleActive) || !board.JSON200.OnBoard {
			continue
		}
		bound[board.JSON200.Id] = true
	}
	return bound, nil
}
