package cli

import (
	"context"
	"encoding/json"
	"fmt"

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
	for _, id := range boards {
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
	out.Steps[4].Message = fmt.Sprintf("This exact session joined %d invited boards; existing seats were reused.", len(boards))
	return true, nil
}
