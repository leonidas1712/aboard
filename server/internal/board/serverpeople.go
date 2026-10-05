package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// The people on the server, their server roles, and removing someone from the server.
// Only an admin changes roles or removes people, always with their own access key, never
// an agent or a browser; the role is read again inside the transaction that acts on it,
// and the server always keeps an admin.

func guestNotAllowed(what string) *apierr.Error {
	return apierr.New(http.StatusForbidden, "guest_not_allowed",
		fmt.Sprintf("A guest can't %s: guests only read and post on the boards they were invited to.", what),
		"Ask a person on the board to do it.")
}

func personIsGuest(handle, hint string) *apierr.Error {
	return apierr.New(http.StatusConflict, "person_is_guest", fmt.Sprintf("%s is a guest of this server.", handle), hint)
}

func lastAdmin(what string) *apierr.Error {
	return apierr.New(http.StatusConflict, "last_admin",
		fmt.Sprintf("That is the server's last admin, who can't be %s: the server always keeps an admin.", what),
		"Make someone else an admin first: aboard people role @name admin.")
}

// adminKey refuses anything but a person's own access key, for what only an admin does
// with their own key. command is what to run in a terminal instead.
func adminKey(p Principal, what, command string) error {
	if err := requireHuman(p); err != nil {
		return err
	}
	if p.Browser {
		return apierr.New(http.StatusForbidden, "human_token_required",
			fmt.Sprintf("A browser can't %s; only your own access key can.", what),
			"Run "+command+" in a terminal.")
	}
	return nil
}

// requireServerAdmin reads the caller again inside tx and refuses unless they are an
// admin of the server now, so one demoted or removed since they authenticated can't act.
func requireServerAdmin(tx ReadTx, p Principal, now, what string) (Human, error) {
	me, err := caller(tx, p, now)
	if err != nil {
		return Human{}, err
	}
	if me.Role != ServerAdmin {
		return Human{}, apierr.New(http.StatusForbidden, "server_admin_required",
			fmt.Sprintf("Only an admin of this server can %s.", what),
			"Ask an admin of this server to do it.")
	}
	return me, nil
}

// ListServerPeople returns the people on the server, oldest first, to a person on it who
// isn't a guest.
func (s *Service) ListServerPeople(ctx context.Context, p Principal) ([]Human, error) {
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	var out []Human
	err := s.st.Read(ctx, func(tx ReadTx) error {
		me, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if me.Role == ServerGuest {
			return guestNotAllowed("list the server's people")
		}
		out, err = tx.PeopleOnServer()
		return err
	})
	return out, err
}

// RoleChange is a person's server role, set.
type RoleChange struct {
	Person  Human
	Changed bool
}

// SetServerRole makes the person called handle an admin of the server, or a member again.
// Only an admin may, with their own key. The last admin stays one, and a guest's role
// doesn't change: upgrading a guest in place is not supported. A server invite creates
// a new identity.
func (s *Service) SetServerRole(ctx context.Context, p Principal, handle, role string) (RoleChange, error) {
	if err := adminKey(p, "change a person's role", "aboard people role @"+handle+" "+role); err != nil {
		return RoleChange{}, err
	}
	if role != ServerAdmin && role != ServerMember {
		return RoleChange{}, invalid(fmt.Sprintf("%q is not a role a person can be given.", role), "Use admin or member.")
	}
	var out RoleChange
	err := s.writeAs(ctx, p, func(tx Tx) error {
		if _, err := requireServerAdmin(tx, p, stamp(s.clk.Now()), "change a person's role"); err != nil {
			return err
		}
		target, err := tx.HumanByName(handle)
		if errors.Is(err, ErrNotFound) {
			return noSuchPerson(handle)
		}
		if err != nil {
			return err
		}
		if target.Role == ServerGuest {
			return personIsGuest(handle, "Upgrading a guest in place is not supported. An admin can remove them and invite a new identity with aboard invite --server; their board owners must add that new person again.")
		}
		out.Person = target
		if target.Role == role {
			return nil
		}
		if target.Role == ServerAdmin {
			admins, err := tx.AdminCount()
			if err != nil {
				return err
			}
			if admins <= 1 {
				return lastAdmin("made a member")
			}
		}
		if err := tx.SetHumanRole(target.ID, role); err != nil {
			return err
		}
		out.Person.Role, out.Changed = role, true
		return nil
	})
	return out, err
}

// Removal is what removing a person from the server did, or would do.
type Removal struct {
	Person          Human
	DryRun          bool
	KeysRevoked     int
	BrowserSessions int
	Agents          int
	Boards          int
	OwnersPassed    int
	// Unreachable lists the private boards no one is left on, by id.
	Unreachable []string
}

// RemoveFromServer removes the person called handle from the server, in one
// transaction: their keys are revoked, so everything those started stops; their browser
// logins end; they are taken off every board they are on, with their agents there and
// the join codes they or their agents made, the record naming the admin; and on a board
// where they were the last owner, the person on it longest who isn't a guest becomes
// owner. Only an admin may, with their own key, and never the server's last admin. With
// dryRun nothing changes and the result says what would.
func (s *Service) RemoveFromServer(ctx context.Context, p Principal, handle string, dryRun bool) (Removal, error) {
	if err := adminKey(p, "remove people from the server", "aboard people remove @"+handle); err != nil {
		return Removal{}, err
	}
	out := Removal{DryRun: dryRun, Unreachable: []string{}}
	var touched []string
	write := func(tx Tx) error {
		now := s.clk.Now()
		me, err := requireServerAdmin(tx, p, stamp(now), "remove people from the server")
		if err != nil {
			return err
		}
		target, err := tx.HumanByName(handle)
		if errors.Is(err, ErrNotFound) {
			return noSuchPerson(handle)
		}
		if err != nil {
			return err
		}
		out.Person = target
		if target.Role == ServerAdmin {
			admins, err := tx.AdminCount()
			if err != nil {
				return err
			}
			if admins <= 1 {
				return lastAdmin("removed")
			}
		}
		keys, err := tx.KeysOf(target.ID, stamp(now))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if !keyWorks(k.AccessKey, stamp(now)) {
				continue
			}
			if err := tx.RevokeAccessKey(k.ID, stamp(now)); err != nil {
				return fmt.Errorf("revoke key %s: %w", k.ID, err)
			}
			out.KeysRevoked++
		}
		if out.BrowserSessions, err = tx.DeleteBrowserLogins(target.ID, stamp(now)); err != nil {
			return err
		}
		boards, err := tx.BoardsOfHuman(target.ID)
		if err != nil {
			return err
		}
		for _, b := range boards {
			if err := s.removeFromBoard(tx, &b, me, target, &out); err != nil {
				return err
			}
			touched = append(touched, b.ID)
		}
		if err := tx.RemoveHuman(target.ID, stamp(now), me.ID); err != nil {
			return err
		}
		if dryRun {
			return errPreview
		}
		return nil
	}
	if err := s.writeAs(ctx, p, write); err != nil && !errors.Is(err, errPreview) {
		return Removal{}, err
	}
	if dryRun {
		return out, nil
	}
	s.notify.Changed(credentialsKey(out.Person.ID))
	s.notify.Changed(boardsOfKey(out.Person.ID))
	for _, id := range touched {
		s.notify.Changed(id)
	}
	return out, nil
}

// removeFromBoard takes a person being removed from the server off b, recording the
// admin who did it, and passes ownership on when they were b's last owner.
func (s *Service) removeFromBoard(tx Tx, b *Board, admin, target Human, out *Removal) error {
	m, err := tx.HumanMember(b.ID, target.ID)
	if err != nil {
		return err
	}
	actor := events.Actor{Kind: "human", Name: ptr(admin.Name)}
	if am, err := tx.HumanMember(b.ID, admin.ID); err == nil && am.Status == StatusActive {
		actor = actorOf(am)
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	agents, err := s.takeOff(tx, b, m, StatusRemoved, events.PersonRemoved, actor, map[string]any{"from_server": true})
	if err != nil {
		return err
	}
	out.Boards++
	out.Agents += len(agents)
	people, err := peopleOn(tx, *b)
	if err != nil {
		return err
	}
	if len(people) == 0 && b.Visibility == BoardPrivate {
		out.Unreachable = append(out.Unreachable, b.ID)
	}
	if m.Access != rules.AccessAdmin || len(ownersOf(people)) > 0 {
		return nil
	}
	for _, heir := range people {
		if heir.Person.Role == ServerGuest {
			continue
		}
		if err := tx.SetMemberAccess(heir.Member.ID, rules.AccessAdmin); err != nil {
			return err
		}
		out.OwnersPassed++
		_, err := s.append(tx, b, events.PersonMadeOwner, events.Actor{Kind: "system"}, s.clk.Now(), map[string]any{
			"member_id": heir.Member.ID, "person_id": heir.Person.ID, "name": heir.Member.Name,
			"reason": "owner_removed_from_server",
		})
		return err
	}
	return nil
}
