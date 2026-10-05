package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
)

// An agent's delivery mode decides when messages wake its session. Its person sets it,
// and the server holds it, so every machine of theirs and the board view agree; the
// delivery daemon running the session reads it with the agent's inbox and applies it.
// Only the agent's person changes it: not the agent, not a board owner, not an admin.

// Delivery modes a person sets for an agent.
const (
	// DeliveryFocused, the default, wakes the agent only for messages that concern it.
	DeliveryFocused = "focused"
	DeliveryAll     = "all"
	DeliveryHumans  = "humans"
	DeliveryOff     = "off"
)

var deliverySettings = []string{DeliveryFocused, DeliveryAll, DeliveryHumans, DeliveryOff}

// DeliverySetting is an agent's delivery mode as its person set it. Mode is empty and
// Seq 0 for an agent whose mode was never set; Seq is otherwise the seq of the
// agent.delivery_changed event that set it, so it only grows.
type DeliverySetting struct {
	Mode string
	Seq  int64
}

// Current returns the mode in force: Mode, or focused when it was never set.
func (d DeliverySetting) Current() string {
	if d.Mode == "" {
		return DeliveryFocused
	}
	return d.Mode
}

// DeliveryChange is an agent's delivery mode after SetDeliveryMode, and whether the call
// changed it.
type DeliveryChange struct {
	Board   Board
	Agent   Member
	Changed bool
}

func agentNotFound(name, board string) *apierr.Error {
	return apierr.New(http.StatusNotFound, "agent_not_found",
		fmt.Sprintf("Board %s has no agent called %s.", board, name),
		"Run aboard board people --board "+board+" or look at the board view to see its agents.")
}

// SetDeliveryMode sets the delivery mode of the agent called agentName on a board. Only
// the agent's person may, with their own key or browser; ownership and access are
// checked inside the write. Setting the mode the agent has changes nothing and writes no
// event.
func (s *Service) SetDeliveryMode(ctx context.Context, p Principal, boardName, agentName, mode string) (DeliveryChange, error) {
	if !slices.Contains(deliverySettings, mode) {
		return DeliveryChange{}, invalid(fmt.Sprintf("%q is not a delivery mode.", mode), "Use focused, all, humans or off.")
	}
	command := fmt.Sprintf("aboard delivery %s --as %s --board %s", mode, agentName, boardName)
	if err := humanOnly(p, "change an agent's delivery mode", command); err != nil {
		return DeliveryChange{}, err
	}
	var out DeliveryChange
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		agent, err := tx.MemberByName(b.ID, agentName)
		if errors.Is(err, ErrNotFound) || (err == nil && (agent.Kind != "agent" || agent.Status != StatusActive)) {
			return agentNotFound(agentName, b.Name)
		}
		if err != nil {
			return err
		}
		// An agent is on the board only while its person is.
		person, err := tx.HumanMember(b.ID, agent.HumanID)
		if errors.Is(err, ErrNotFound) || (err == nil && person.Status != StatusActive) {
			return agentNotFound(agentName, b.Name)
		}
		if err != nil {
			return err
		}
		if agent.HumanID != p.Human.ID {
			return apierr.New(http.StatusForbidden, "agent_owner_required",
				fmt.Sprintf("Only %s, whose agent it is, can change %s's delivery mode.", person.Name, agent.Name),
				fmt.Sprintf("Ask %s to run: %s", person.Name, command))
		}
		out = DeliveryChange{Board: b, Agent: agent}
		before := agent.Delivery.Current()
		if before == mode {
			return nil
		}
		e, err := s.append(tx, &b, events.AgentDeliveryChanged, actorOf(me), s.clk.Now(), map[string]any{
			"member_id": agent.ID, "name": agent.Name, "before": before, "after": mode,
		})
		if err != nil {
			return err
		}
		agent.Delivery = DeliverySetting{Mode: mode, Seq: e.Seq}
		if err := tx.SetDelivery(agent.ID, agent.Delivery); err != nil {
			return fmt.Errorf("set delivery mode: %w", err)
		}
		out = DeliveryChange{Board: b, Agent: agent, Changed: true}
		return nil
	})
	if err != nil {
		return DeliveryChange{}, err
	}
	if out.Changed {
		// The board's head moved: its followers, the agent's delivery daemon among them,
		// read the agent's inbox again, which carries the new mode.
		s.notify.Changed(out.Board.ID)
	}
	return out, nil
}
