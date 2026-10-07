package board

import (
	"fmt"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func (s *Service) addPersonAuthority(tx ReadTx, p Principal, name, handle string) (Board, Member, bool, error) {
	b, me, on, err := s.see(tx, p, name)
	if err != nil {
		return Board{}, Member{}, false, err
	}
	if err := requireActive(b); err != nil {
		return Board{}, Member{}, false, err
	}
	person, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return Board{}, Member{}, false, err
	}
	if person.Role == ServerGuest {
		return Board{}, Member{}, false, guestNotAllowed("add people to a board")
	}
	if p.Agent != nil {
		if me.Session == nil || *me.Session == "" {
			return Board{}, Member{}, false, apierr.New(http.StatusForbidden, "agent_session_required", "Only an agent backed by a harness session can add people.", "Ask your person to run: aboard board add @"+handle+" --board "+b.Name)
		}
		allowed, err := tx.AgentsAddPeople()
		if err != nil {
			return Board{}, Member{}, false, err
		}
		if !allowed || !b.AgentsAddPeople || me.Role == nil || !b.Roles[*me.Role].Has(rules.AddPeople) {
			return Board{}, Member{}, false, apierr.New(http.StatusForbidden, "add_people_not_allowed", fmt.Sprintf("Your agent may not add people to board %s: its server, board or role does not allow it.", b.Name), "Ask your person to run: aboard board add @"+handle+" --board "+b.Name)
		}
	}
	return b, me, on, nil
}

func addPersonProvenance(p Principal) map[string]any {
	if p.Agent == nil {
		return nil
	}
	return map[string]any{"by_owner": p.Agent.HumanID}
}
