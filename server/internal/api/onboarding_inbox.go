package api

import (
	"context"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (h *handlers) ListOnboardingInbox(ctx context.Context, _ ListOnboardingInboxRequestObject) (ListOnboardingInboxResponseObject, error) {
	inbox, err := h.svc.OnboardingInbox(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	adds := []map[string]any{}
	for _, n := range inbox.BoardAdds {
		command := onboardingCommand(ctx, "aboard join '"+strings.ReplaceAll(n.Board.Name, "'", "'\\''")+"'")
		adds = append(adds, map[string]any{"board": inboxBoard(n.Board), "added": map[string]any{"seq": n.Added.Seq, "at": n.Added.At, "by": n.Added.Actor}, "join_command": command, "join_prompt": "Run " + command + " in this session, then say hello on the board."})
	}
	arrivals := []map[string]any{}
	for _, n := range inbox.Arrivals {
		boards := []map[string]any{}
		for _, b := range n.Boards {
			agents := []map[string]any{}
			for _, a := range b.Agents {
				harness := ""
				if a.Harness != nil {
					harness = *a.Harness
				}
				agents = append(agents, map[string]any{"id": a.ID, "name": a.Name, "harness": harness})
			}
			boards = append(boards, map[string]any{"board": inboxBoard(b.Board), "agents": agents})
		}
		arrivals = append(arrivals, map[string]any{"invite_id": n.InviteID, "person_id": n.Person.ID, "handle": n.Person.Name, "at": n.At, "boards": boards})
	}
	return convert[ListOnboardingInbox200JSONResponse](map[string]any{"board_adds": adds, "arrivals": arrivals})
}

func inboxBoard(b board.Board) map[string]any {
	return map[string]any{"id": b.ID, "name": b.Name, "title": b.Title}
}
