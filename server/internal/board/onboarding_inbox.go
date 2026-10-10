package board

import (
	"context"
	"errors"
	"time"

	"github.com/leonidas1712/aboard/server/internal/events"
)

type BoardAddNotice struct {
	Board Board
	Added events.Event
}

type ArrivalBoard struct {
	Board  Board
	Agents []Member
}

type InviteArrivalNotice struct {
	InviteID string
	Person   Human
	At       string
	Boards   []ArrivalBoard
}

type OnboardingInbox struct {
	BoardAdds []BoardAddNotice
	Arrivals  []InviteArrivalNotice
}

// OnboardingInbox reads existing membership and invitation records, without dispatching agents.
func (s *Service) OnboardingInbox(ctx context.Context, p Principal) (OnboardingInbox, error) {
	var out OnboardingInbox
	if err := humanOnly(p, "read your onboarding Inbox", "aboard boards"); err != nil {
		return out, err
	}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		boards, err := tx.MembershipBoards(person.ID)
		if err != nil {
			return err
		}
		for _, b := range boards {
			if lifecycleOf(b) != LifecycleActive {
				continue
			}
			_, me, on, err := s.see(tx, p, b.Name)
			if err != nil || !on {
				continue
			}
			members, err := tx.Members(b.ID)
			if err != nil {
				return err
			}
			added, err := addedBy(tx, b, me, members)
			if err != nil {
				return err
			}
			if added != nil {
				out.BoardAdds = append(out.BoardAdds, BoardAddNotice{Board: b, Added: *added})
			}
		}
		invites, err := tx.ServerInvites(person.ID)
		if err != nil {
			return err
		}
		cutoff := stamp(s.clk.Now().Add(-7 * 24 * time.Hour))
		for _, i := range invites {
			if i.UsedAt == nil || i.UsedBy == nil || *i.UsedAt < cutoff {
				continue
			}
			recipient, err := tx.HumanByID(*i.UsedBy)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if recipient.RemovedAt != nil {
				continue
			}
			arrival := InviteArrivalNotice{InviteID: i.ID, Person: recipient, At: *i.UsedAt}
			for _, id := range i.Boards {
				b, err := tx.BoardByID(id)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if lifecycleOf(b) != LifecycleActive {
					continue
				}
				if _, _, on, err := s.see(tx, p, b.Name); err != nil || !on {
					continue
				}
				membership, err := tx.HumanMember(b.ID, recipient.ID)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if membership.Status != StatusActive {
					continue
				}
				members, err := tx.Members(b.ID)
				if err != nil {
					return err
				}
				joined := ArrivalBoard{Board: b}
				for _, m := range members {
					if m.Kind == "agent" && m.HumanID == recipient.ID && m.Status == StatusActive && m.JoinedAt >= *i.UsedAt {
						joined.Agents = append(joined.Agents, m)
					}
				}
				if len(joined.Agents) > 0 {
					arrival.Boards = append(arrival.Boards, joined)
				}
			}
			if len(arrival.Boards) > 0 {
				out.Arrivals = append(out.Arrivals, arrival)
			}
		}
		return nil
	})
	return out, err
}
