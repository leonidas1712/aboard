package board

import (
	"context"
	"errors"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// Mid-turn policies control which same-owner senders may reach a busy agent.
const (
	MidturnOwnerOnly = "owner-only"
	MidturnMyAgents  = "my-agents"
)

// MidturnOverride binds a preference to an immutable agent seat.
type MidturnOverride struct {
	MemberID string
	Policy   string
}

// MidturnView contains only the caller's default or effective preference.
type MidturnView struct {
	Policy, Source string
	Overrides      []MidturnOverride
	Changed        bool
}

func effectiveMidturn(person Human, agent Member) (policy, source string) {
	if agent.MidturnOverride != nil {
		return *agent.MidturnOverride, "agent_override"
	}
	policy = person.MidturnPolicy
	if policy == "" {
		policy = MidturnMyAgents
	}
	return policy, "person_default"
}

func (s *Service) midturnTarget(tx ReadTx, p Principal, memberID string) (Member, error) {
	m, err := tx.MemberByID(memberID)
	if errors.Is(err, ErrNotFound) || (err == nil && (m.Kind != "agent" || m.Status != StatusActive)) {
		return Member{}, apierr.New(404, "member_not_found", "That active agent is not available.", "Choose one of your active agents.")
	}
	if err != nil {
		return Member{}, err
	}
	b, err := tx.BoardByID(m.BoardID)
	if err != nil {
		return Member{}, err
	}
	_, _, _, err = s.see(tx, p, b.Name)
	if isBoardNotFound(err) {
		return Member{}, apierr.New(404, "member_not_found", "That active agent is not available.", "Choose one of your active agents.")
	}
	if err != nil {
		return Member{}, err
	}
	owner, err := tx.HumanMember(b.ID, m.HumanID)
	if errors.Is(err, ErrNotFound) || (err == nil && owner.Status != StatusActive) {
		return Member{}, apierr.New(404, "member_not_found", "That active agent is not available.", "Choose one of your active agents.")
	}
	if err != nil {
		return Member{}, err
	}
	if m.HumanID != p.Human.ID {
		return Member{}, apierr.New(http.StatusForbidden, "agent_owner_required", "Only the agent's own person may set its mid-turn policy.", "Ask its person to change aboard delivery midturn.")
	}
	return m, nil
}

func midturnView(tx ReadTx, p Principal, now string) (MidturnView, error) {
	person, err := caller(tx, p, now)
	if err != nil {
		return MidturnView{}, err
	}
	if p.Agent != nil {
		_, me, err := seatOf(tx, *p.Agent)
		if err != nil {
			return MidturnView{}, err
		}
		policy, source := effectiveMidturn(person, me)
		return MidturnView{Policy: policy, Source: source}, nil
	}
	if p.Human == nil {
		return MidturnView{}, humanOnly(p, "read mid-turn settings", "aboard delivery midturn")
	}
	policy, source := effectiveMidturn(person, Member{})
	out := MidturnView{Policy: policy, Source: source, Overrides: []MidturnOverride{}}
	boards, err := tx.BoardsOfHuman(person.ID)
	if err != nil {
		return MidturnView{}, err
	}
	for _, b := range boards {
		members, err := tx.Members(b.ID)
		if err != nil {
			return MidturnView{}, err
		}
		for _, m := range members {
			if m.Kind == "agent" && m.Status == StatusActive && m.HumanID == person.ID && m.MidturnOverride != nil {
				out.Overrides = append(out.Overrides, MidturnOverride{MemberID: m.ID, Policy: *m.MidturnOverride})
			}
		}
	}
	return out, nil
}

// MidturnPolicy reads current preferences without granting board access.
func (s *Service) MidturnPolicy(ctx context.Context, p Principal) (MidturnView, error) {
	var out MidturnView
	err := s.st.Read(ctx, func(tx ReadTx) error { var err error; out, err = midturnView(tx, p, stamp(s.clk.Now())); return err })
	return out, err
}

// SetMidturnPolicy checks current ownership in the preference write transaction.
func (s *Service) SetMidturnPolicy(ctx context.Context, p Principal, memberID string, policy *string) (MidturnView, error) {
	if err := humanOnly(p, "change mid-turn delivery", "aboard delivery midturn owner-only"); err != nil {
		return MidturnView{}, err
	}
	if policy == nil && memberID == "" || policy != nil && *policy != MidturnOwnerOnly && *policy != MidturnMyAgents {
		return MidturnView{}, invalid("Choose a mid-turn policy.", "Use owner-only or my-agents; clear only an agent override.")
	}
	var out MidturnView
	err := s.writeAs(ctx, p, func(tx Tx) error {
		changed := false
		if memberID == "" {
			person, err := tx.HumanByID(p.Human.ID)
			if err != nil {
				return err
			}
			before, _ := effectiveMidturn(person, Member{})
			changed = before != *policy
			if changed {
				if err := tx.SetHumanMidturn(person.ID, *policy); err != nil {
					return err
				}
			}
		} else {
			agent, err := s.midturnTarget(tx, p, memberID)
			if err != nil {
				return err
			}
			changed = (agent.MidturnOverride == nil) != (policy == nil) || (agent.MidturnOverride != nil && policy != nil && *agent.MidturnOverride != *policy)
			if changed {
				if err := tx.SetAgentMidturn(memberID, policy); err != nil {
					return err
				}
			}
		}
		var err error
		out, err = midturnView(tx, p, stamp(s.clk.Now()))
		out.Changed = changed
		return err
	})
	return out, err
}

// CheckMidturnReplay refuses retained receipts after their target stops being owned or visible.
func (s *Service) CheckMidturnReplay(ctx context.Context, p Principal, memberID string) error {
	if err := humanOnly(p, "change mid-turn delivery", "aboard delivery midturn owner-only"); err != nil {
		return err
	}
	return s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		if memberID != "" {
			_, err := s.midturnTarget(tx, p, memberID)
			return err
		}
		return nil
	})
}
