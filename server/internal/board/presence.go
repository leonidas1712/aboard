package board

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// Presence states: what an agent's session is doing.
const (
	// PresenceWorking means a turn is running in the agent's session.
	PresenceWorking = "working"
	// PresenceIdle means the session is open and waiting for messages.
	PresenceIdle = "idle"
	// PresenceWaiting means the harness is waiting for a person in the session.
	PresenceWaiting = "waiting"
	// PresenceNoSession means no session is open for the agent.
	PresenceNoSession = "no_session"
)

var presenceStates = []string{PresenceWorking, PresenceIdle, PresenceWaiting, PresenceNoSession}

// PresenceTTL is how long a reported presence holds without being reported again. The
// delivery daemon renews it well within this, so it runs out only when the daemon or
// its machine has gone, and the agent then reads as having no session.
const PresenceTTL = 3 * time.Minute

// Presence is what an agent's owner's delivery daemon last reported about its session.
// Like a read position it is bookkeeping, not board content: it is never an event.
type Presence struct {
	State string // one of the Presence* states; empty for a person, or never reported
	Since string // when State began
	At    string // when it was last reported
	// Delivery is the agent's delivery mode (auto, humans or off) as last reported, or
	// empty if none was. It doesn't run out with the presence.
	Delivery string
}

var deliveryModes = []string{"auto", "humans", "off"}

// presenceKey is the Notifier key that changes when an agent's presence on the board
// changes. Board ids start with "brd_", so it never names a board.
func presenceKey(boardID string) string { return "presence/" + boardID }

// CurrentPresence returns the member's presence as members see it at now: none for a
// person, no_session for an agent never reported, and no_session, since it was last
// reported, for a presence that ran out.
func (m Member) CurrentPresence(now time.Time) Presence {
	if m.Kind != "agent" {
		return Presence{}
	}
	p := m.Presence
	if p.State == "" {
		return Presence{State: PresenceNoSession, Delivery: p.Delivery}
	}
	at, err := time.Parse(time.RFC3339Nano, p.At)
	if err != nil || !now.Before(at.Add(PresenceTTL)) {
		return Presence{State: PresenceNoSession, Since: p.At, At: p.At, Delivery: p.Delivery}
	}
	return p
}

// SetPresence records the calling agent's presence and, unless mode is empty, its
// delivery mode, and returns its board and its membership with the new presence. Only
// agents report presence, each its own. Reporting the presence it already has renews it
// and keeps when it began.
func (s *Service) SetPresence(ctx context.Context, p Principal, state, mode string) (Board, Member, error) {
	if p.Agent == nil {
		return Board{}, Member{}, apierr.AgentRequired()
	}
	if !slices.Contains(presenceStates, state) {
		return Board{}, Member{}, invalid(fmt.Sprintf("%q is not a presence.", state), "Use working, idle, waiting or no_session.")
	}
	if mode != "" && !slices.Contains(deliveryModes, mode) {
		return Board{}, Member{}, invalid(fmt.Sprintf("%q is not a delivery mode.", mode), "Use auto, humans or off.")
	}
	now := s.clk.Now()
	var b Board
	var me Member
	changed := false
	err := s.st.Write(ctx, func(tx Tx) error {
		var err error
		if b, err = tx.BoardByID(p.Agent.BoardID); err != nil {
			return err
		}
		if me, err = tx.MemberByName(b.ID, p.Agent.Name); err != nil {
			return err
		}
		cur := me.CurrentPresence(now)
		next := Presence{State: state, Since: cur.Since, At: stamp(now), Delivery: me.Presence.Delivery}
		if mode != "" {
			next.Delivery = mode
		}
		if cur.State != state || cur.Since == "" {
			next.Since = next.At
		}
		changed = cur.State != state
		me.Presence = next
		return tx.SetPresence(me.ID, next)
	})
	if err != nil {
		return Board{}, Member{}, fmt.Errorf("set presence: %w", err)
	}
	if changed {
		s.notify.Changed(presenceKey(b.ID))
	}
	return b, me, nil
}

// PresenceChange is an agent's presence that changed since a HeadFeed last looked.
type PresenceChange struct {
	BoardID  string
	Board    string // the board's name
	Agent    string
	Presence Presence
}

// presenceOn returns the current presence of every agent on each board, by board id and
// agent name.
func (s *Service) presenceOn(ctx context.Context, boardIDs []string) (map[string]map[string]Presence, error) {
	now := s.clk.Now()
	out := make(map[string]map[string]Presence, len(boardIDs))
	err := s.st.Read(ctx, func(tx ReadTx) error {
		for _, id := range boardIDs {
			members, err := tx.Members(id)
			if err != nil {
				return err
			}
			agents := map[string]Presence{}
			for _, m := range members {
				if m.Kind == "agent" {
					agents[m.Name] = m.CurrentPresence(now)
				}
			}
			out[id] = agents
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read presence: %w", err)
	}
	return out, nil
}
