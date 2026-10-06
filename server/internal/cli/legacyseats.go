package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// preflightNewSeat checks the session before a pairing or code redemption can
// create remote resources or save a credential. A pasted board name is not proof
// that a code replaces an existing seat.
func (a *app) preflightNewSeat(ctx context.Context, key delivery.SessionKey, server string) error {
	response, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID})
	if err != nil {
		return err
	}
	for _, agent := range response.Agents {
		if agent.Server != server {
			e := newError("session_on_another_server", "This session already has seats on another server.", "Use a session for that server.")
			e.Details = map[string]any{"session_server": agent.Server}
			return e
		}
	}
	if key.Harness == "omp" && len(response.Agents) > 0 && !slices.Contains(response.Capabilities, delivery.CapabilityHandoffV1) {
		return newError("extension_outdated", "This extension cannot receive a new seat while this session is already bound.", "Run aboard init in a terminal and restart the harness, or join from a fresh session.")
	}
	return nil
}

func (a *app) seatBoardReminder(ctx context.Context, key delivery.SessionKey, board string) string {
	if key.ID == "" {
		return ""
	}
	agents, err := a.sessionAgents(ctx, key)
	if err != nil || len(agents) < 2 {
		return ""
	}
	return fmt.Sprintf("This session has seats on several boards. Use --board %s for commands on this board.\n", shellWord(board))
}
