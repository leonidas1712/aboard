package board

import (
	"context"
	"errors"
	"unicode"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// AgentLocation is a session's last reported location, never authority or liveness proof.
type AgentLocation struct {
	Machine, Harness, SessionID, Folder, LastActive string
}

// LocatedAgent names the board of an agent owned by the caller's person.
type LocatedAgent struct {
	BoardName string
	Member    Member
}

// ListOwnAgents reads only the caller's current person's active seats on their boards.
func (s *Service) ListOwnAgents(ctx context.Context, p Principal) ([]LocatedAgent, error) {
	out := []LocatedAgent{}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		now := s.clk.Now()
		person, err := caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		if p.Agent != nil {
			if _, _, err := seatOf(tx, *p.Agent); err != nil {
				return err
			}
		}
		boards, err := tx.BoardsOfHuman(person.ID)
		if err != nil {
			return err
		}
		for _, b := range boards {
			if lifecycleOf(b) == LifecycleDeleted || person.Role == ServerGuest && p.Agent != nil && b.ID != p.Agent.BoardID {
				continue
			}
			members, err := tx.Members(b.ID)
			if err != nil {
				return err
			}
			for _, m := range members {
				if m.Kind != "agent" || m.Status != StatusActive || m.HumanID != person.ID {
					continue
				}
				working, err := agentKeyCurrent(tx, m, stamp(now))
				if err != nil {
					return err
				}
				if !working {
					continue
				}
				m.Presence = m.CurrentPresence(now)
				m.Location, err = ownLocation(tx, m)
				if err != nil {
					return err
				}
				if p.Agent != nil && !b.Policy.ShowHarness {
					m.Harness = nil
				}
				out = append(out, LocatedAgent{BoardName: b.Name, Member: m})
			}
		}
		return nil
	})
	return out, err
}

func agentKeyCurrent(tx ReadTx, m Member, now string) (bool, error) {
	if m.KeyID == nil {
		return true, nil
	}
	key, err := tx.AccessKeyByID(*m.KeyID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return key.HumanID == m.HumanID && keyWorks(key, now), nil
}

func ownLocation(tx ReadTx, m Member) (*AgentLocation, error) {
	loc, err := tx.AgentLocation(m.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &loc, nil
}

// SetAgentLocation saves only the calling seat's descriptive session report.
func (s *Service) SetAgentLocation(ctx context.Context, p Principal, in AgentLocation) (AgentLocation, error) {
	if p.Agent == nil {
		return AgentLocation{}, apierr.AgentRequired()
	}
	for _, field := range []struct {
		value string
		limit int
	}{{in.Harness, 40}, {in.SessionID, 512}, {in.Folder, 4096}} {
		if field.value == "" || !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit {
			return AgentLocation{}, apierr.New(400, "invalid_request", "The agent location is incomplete or too long.", "Report a harness, conversation id and working folder.")
		}
		for _, r := range field.value {
			if unicode.IsControl(r) {
				return AgentLocation{}, apierr.New(400, "invalid_request", "An agent location cannot contain control characters.", "Remove control characters from the location report.")
			}
		}
	}
	var out AgentLocation
	err := s.writeBookkeepingAs(ctx, p, func(tx Tx) error {
		_, me, err := seatOf(tx, *p.Agent)
		if err != nil {
			return err
		}
		out = AgentLocation{Harness: in.Harness, SessionID: in.SessionID, Folder: in.Folder, LastActive: stamp(s.clk.Now())}
		if me.KeyID != nil {
			key, err := tx.AccessKeyByID(*me.KeyID)
			if err != nil {
				return err
			}
			out.Machine = key.Name
		}
		return tx.SetAgentLocation(me.ID, out)
	})
	return out, err
}
