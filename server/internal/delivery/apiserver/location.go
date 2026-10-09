package apiserver

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// ReportLocation uses the seat's own issuing credential, never a person login.
func (s *Server) ReportLocation(ctx context.Context, agent delivery.AgentRef, location delivery.SessionLocation) error {
	if agent.Server != s.url {
		return delivery.ErrUnauthorized
	}
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return err
	}
	c, err := s.bookkeepingClient(token)
	if err != nil {
		return err
	}
	r, err := c.SetAgentLocationWithResponse(ctx, &api.SetAgentLocationParams{}, api.AgentLocationReport{Harness: location.Harness, SessionId: location.SessionID, Folder: location.Folder})
	if err != nil {
		return fmt.Errorf("report agent location: %w", err)
	}
	if r.JSON200 == nil {
		return statusError("report agent location", r.StatusCode(), r.Body)
	}
	return nil
}
