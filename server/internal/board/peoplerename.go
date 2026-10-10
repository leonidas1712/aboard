package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
)

// HandleTaken does not reveal which person or board holds the handle.
func HandleTaken(name string) error {
	return apierr.New(http.StatusConflict, "handle_taken", fmt.Sprintf("The handle %s is taken or reserved.", name), "Pick another handle.")
}

func renameAuthority(tx ReadTx, p Principal, targetID, now string) error {
	me, err := caller(tx, p, now)
	if err != nil {
		return err
	}
	if me.ID != targetID && (me.Role != ServerAdmin || p.Agent != nil || p.Browser) {
		return apierr.New(http.StatusForbidden, "server_admin_required", "Only this person or a server admin can rename them.", "Ask the person or a server admin to run aboard people rename.")
	}
	return nil
}

// RenamePerson changes only handle projections; permanent ids and credentials stay.
func (s *Service) RenamePerson(ctx context.Context, p Principal, handle, name string) (RoleChange, error) {
	if err := renameCredential(p); err != nil {
		return RoleChange{}, err
	}
	if !validName(name) {
		return RoleChange{}, invalid("A handle uses lowercase letters, digits and single dashes, at most 40 characters.", "Pick a handle such as leo.")
	}
	var out RoleChange
	var touched []string
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		// Check authority before looking up another person's handle.
		me, err := caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		if p.Agent != nil {
			b, _, err := seatOf(tx, *p.Agent)
			if err != nil {
				return err
			}
			if err := requireActive(b); err != nil {
				return err
			}
		}
		if me.Name != handle && (me.Role != ServerAdmin || p.Agent != nil || p.Browser) {
			err := renameAuthority(tx, p, "", stamp(now))
			return err
		}
		target, err := tx.HumanByName(handle)
		if errors.Is(err, ErrNotFound) {
			return apierr.New(http.StatusNotFound, "person_not_found", "That person isn't on this server.", "Run aboard people to list its people.")
		}
		if err != nil {
			return err
		}
		if err := renameAuthority(tx, p, target.ID, stamp(now)); err != nil {
			return err
		}
		out.Person = target
		if target.Name == name {
			return nil
		}
		boards, err := tx.MembershipBoards(target.ID)
		if err != nil {
			return err
		}
		if err := tx.RenameHuman(target.ID, name); err != nil {
			return err
		}
		out.Person.Name, out.Changed = name, true
		actor := events.Actor{Kind: "human", Name: ptr(me.Name)}
		if p.Agent != nil {
			actor = events.Actor{Kind: "agent", Name: ptr(p.Agent.Name), MemberID: ptr(p.Agent.ID)}
		}
		if me.ID == target.ID && p.Agent == nil {
			actor.Name = ptr(name)
		}
		for _, b := range boards {
			member, err := tx.HumanMember(b.ID, target.ID)
			if err != nil {
				return err
			}
			by, err := tx.HumanMember(b.ID, me.ID)
			switch {
			case p.Agent != nil:
				// The authenticated agent remains the actor across person projections.
			case err == nil:
				actor.MemberID = ptr(by.ID)
			case errors.Is(err, ErrNotFound):
				actor.MemberID = nil
			default:
				return err
			}
			if _, err := s.append(tx, &b, "person.renamed", actor, now, map[string]any{"person_id": target.ID, "member_id": member.ID, "before": target.Name, "after": name}); err != nil {
				return err
			}
			touched = append(touched, b.ID)
		}
		return nil
	})
	if err == nil {
		for _, id := range touched {
			s.notify.Changed(id)
		}
		s.notify.Changed(boardsOfKey(out.Person.ID))
	}
	return out, err
}

// CheckRenameReplay rechecks the current credential and authority using permanent ids.
func (s *Service) CheckRenameReplay(ctx context.Context, p Principal, id string) error {
	if err := renameCredential(p); err != nil {
		return err
	}
	return s.st.Read(ctx, func(tx ReadTx) error {
		if p.Agent != nil {
			b, _, err := seatOf(tx, *p.Agent)
			if err != nil {
				return err
			}
			if err := requireActive(b); err != nil {
				return err
			}
		}
		if err := renameAuthority(tx, p, id, stamp(s.clk.Now())); err != nil {
			return err
		}
		if id != "" {
			target, err := tx.HumanByID(id)
			if err != nil {
				return err
			}
			if target.RemovedAt != nil {
				return apierr.New(http.StatusNotFound, "person_not_found", "That person isn't on this server.", "Run aboard people.")
			}
		}
		return nil
	})
}

func renameCredential(p Principal) error {
	if p.Delegation != nil || p.Pairing != nil || (p.Agent == nil && p.Human == nil) {
		return apierr.New(http.StatusForbidden, "human_token_required", "Use your own person or active agent credential to rename yourself.", "Run aboard people rename with your current handle.")
	}
	return nil
}
