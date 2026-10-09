package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// The people on a board, and who sees it. Each person on a board is an owner (the
// creator, and whoever an owner makes one) or a member. Anyone on a board may add people
// to it; only owners remove people or turn the board open or private, and a board always
// keeps an owner. An owner is a person with admin access on the board, so owners also
// change its charter, roles and policy.

// Person is a person on a board: their membership and who they are on the server.
type Person struct {
	Member Member
	Person Human
}

// IsOwner reports whether the person is one of the board's owners.
func (bp Person) IsOwner() bool { return bp.Member.Access == rules.AccessAdmin }

// People is a board with the people on it, in the order they first came onto it.
type People struct {
	Board  Board
	People []Person
}

// peopleOn returns the people on b now.
func peopleOn(tx ReadTx, b Board) ([]Person, error) {
	members, err := tx.Members(b.ID)
	if err != nil {
		return nil, err
	}
	var out []Person
	for _, m := range members {
		if m.Kind != "human" || m.Status != StatusActive {
			continue
		}
		h, err := tx.HumanByID(m.HumanID)
		if err != nil {
			return nil, fmt.Errorf("person %s on board %s: %w", m.HumanID, b.ID, err)
		}
		out = append(out, Person{Member: m, Person: h})
	}
	return out, nil
}

// People lists the people on a board the caller can see.
func (s *Service) People(ctx context.Context, p Principal, boardName string) (People, error) {
	var out People
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, _, _, err := s.see(tx, p, boardName)
		if err != nil {
			return err
		}
		people, err := peopleOn(tx, b)
		out = People{Board: b, People: people}
		return err
	})
	return out, err
}

// addedBy is how a person learns someone else put them on b, read from the record: the
// latest person.added for them, when its actor isn't them, none of their agents has
// joined since, and they haven't read past it. Nil otherwise.
func addedBy(tx ReadTx, b Board, me Member, members []Member) (*events.Event, error) {
	e, err := tx.PersonAdded(b.ID, me.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil || e.Actor.MemberID == nil || *e.Actor.MemberID == me.ID || me.Cursor > e.Seq {
		return nil, err
	}
	for _, m := range members {
		if m.Kind == "agent" && m.HumanID == me.HumanID && m.JoinedAt >= e.At {
			return nil, nil
		}
	}
	return &e, nil
}

func noSuchPerson(handle string) *apierr.Error {
	return apierr.New(http.StatusNotFound, "person_not_found",
		fmt.Sprintf("No one on this server is called %s.", handle),
		"Check the handle; it is the name the person connected with.")
}

func personNotOnBoard(handle, board string) *apierr.Error {
	return apierr.New(http.StatusNotFound, "person_not_on_board",
		fmt.Sprintf("%s isn't on board %s.", handle, board),
		"Run aboard board people --board "+board+" to see who is.")
}

// humanOnly refuses an agent for actions reserved to people. The hint gives
// the command to hand the agent's person.
func humanOnly(p Principal, what, command string) error {
	if p.Human != nil {
		return nil
	}
	return apierr.New(http.StatusForbidden, "human_token_required",
		fmt.Sprintf("Only a person can %s.", what),
		"Ask your person to run: "+command)
}

// AddPerson puts a person on the server onto a board as a member. A person on the
// board, or a permitted session agent, may add them; a person outside an open board
// may add only themselves, which is joining
// it. A person who left or was removed comes back under their old member id and name.
func (s *Service) AddPerson(ctx context.Context, p Principal, boardName, handle string) (Person, error) {
	var out Person
	var b Board
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var me Member
		var on bool
		var err error
		if b, me, on, err = s.addPersonAuthority(tx, p, boardName, handle); err != nil {
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
			return personIsGuest(handle, "A guest comes onto a board only through a guest code for it: aboard invite --guest "+handle+" --board "+b.Name+".")
		}
		if !on && target.ID != p.personID() {
			return notOnBoard(b.Name, p.Human.Name)
		}
		now := s.clk.Now()
		existing, err := tx.HumanMember(b.ID, target.ID)
		switch {
		case err == nil && existing.Status == StatusActive:
			return apierr.New(http.StatusConflict, "already_on_board",
				fmt.Sprintf("%s is already on board %s.", handle, b.Name),
				"Run aboard board people --board "+b.Name+" to see who is.")
		case err == nil:
			actor := actorOf(me)
			if !on {
				actor = actorOf(existing)
			}
			if err := s.restorePerson(tx, &b, &existing, actor, now, addPersonProvenance(p)); err != nil {
				return err
			}
			out = Person{Member: existing, Person: target}
			return nil
		case !errors.Is(err, ErrNotFound):
			return err
		}
		out, err = s.admitNewPerson(tx, &b, me, target, on, addPersonProvenance(p))
		return err
	})
	if err != nil {
		return Person{}, err
	}
	s.notify.Changed(b.ID)
	s.notify.Changed(boardsOfKey(out.Person.ID))
	return out, nil
}

// restorePerson puts back on b a person who left or was removed, as a member, and
// records it.
func (s *Service) restorePerson(tx Tx, b *Board, m *Member, actor events.Actor, at time.Time, extra map[string]any) error {
	if err := tx.SetMemberStatus(m.ID, StatusActive); err != nil {
		return err
	}
	if err := tx.SetMemberAccess(m.ID, rules.AccessMember); err != nil {
		return err
	}
	m.Status, m.Access = StatusActive, rules.AccessMember
	data := map[string]any{"member_id": m.ID, "person_id": m.HumanID, "name": m.Name, "access": m.Access, "rejoined": true}
	for k, v := range extra {
		data[k] = v
	}
	if _, err := s.append(tx, b, events.PersonAdded, actor, at, data); err != nil {
		return err
	}
	// Rejoining restores access, not evidence of reading messages.
	return nil
}

// ownersOf returns the names of the board's owners.
func ownersOf(people []Person) []string {
	var names []string
	for _, bp := range people {
		if bp.IsOwner() {
			names = append(names, bp.Member.Name)
		}
	}
	return names
}

// requireOwner refuses unless me is one of b's owners; what says what they tried to do,
// and the hint names the owners to ask.
func requireOwner(tx ReadTx, b Board, me Member, what string) error {
	if me.Access == rules.AccessAdmin && me.Status == StatusActive {
		return nil
	}
	people, err := peopleOn(tx, b)
	if err != nil {
		return err
	}
	return apierr.New(http.StatusForbidden, "owner_required",
		fmt.Sprintf("Only an owner of board %s can %s.", b.Name, what),
		fmt.Sprintf("Ask an owner to do it: %s.", strings.Join(ownersOf(people), ", ")))
}

// onBoardByHandle finds a person on b by handle.
func onBoardByHandle(tx ReadTx, b Board, handle string) (Person, error) {
	h, err := tx.HumanByName(handle)
	if errors.Is(err, ErrNotFound) {
		return Person{}, personNotOnBoard(handle, b.Name)
	}
	if err != nil {
		return Person{}, err
	}
	m, err := tx.HumanMember(b.ID, h.ID)
	if errors.Is(err, ErrNotFound) || (err == nil && m.Status != StatusActive) {
		return Person{}, personNotOnBoard(handle, b.Name)
	}
	if err != nil {
		return Person{}, err
	}
	return Person{Member: m, Person: h}, nil
}

// RemovePerson takes a person off a board. Only owners may; removing yourself is
// leaving. The person and their agents lose access at once, and the join codes they or
// their agents made for the board stop working.
func (s *Service) RemovePerson(ctx context.Context, p Principal, boardName, handle string) (Person, error) {
	if err := humanOnly(p, "remove people from a board", "aboard board remove @"+handle+" --board "+boardName); err != nil {
		return Person{}, err
	}
	var out Person
	var b Board
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var me Member
		var err error
		if b, me, err = s.access(tx, p, boardName); err != nil {
			return err
		}
		if out, err = onBoardByHandle(tx, b, handle); err != nil {
			return err
		}
		if out.Member.ID == me.ID {
			return s.leave(tx, &b, me)
		}
		if err := requireOwner(tx, b, me, "remove people from it"); err != nil {
			return err
		}
		_, err = s.takeOff(tx, &b, out.Member, StatusRemoved, events.PersonRemoved, actorOf(me), nil)
		return err
	})
	if err != nil {
		return Person{}, err
	}
	out.Member.Status = StatusRemoved
	s.notify.Changed(b.ID)
	s.notify.Changed(boardsOfKey(out.Person.ID))
	return out, nil
}

// Leave takes the calling person off a board. The board's last owner can't leave.
func (s *Service) Leave(ctx context.Context, p Principal, boardName string) (Person, error) {
	if err := humanOnly(p, "leave a board for a person", "aboard board leave --board "+boardName); err != nil {
		return Person{}, err
	}
	var out Person
	var b Board
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var me Member
		var err error
		if b, me, err = s.access(tx, p, boardName); err != nil {
			return err
		}
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		out = Person{Member: me, Person: h}
		return s.leave(tx, &b, me)
	})
	if err != nil {
		return Person{}, err
	}
	out.Member.Status = StatusLeft
	s.notify.Changed(b.ID)
	s.notify.Changed(boardsOfKey(p.Human.ID))
	return out, nil
}

// leave takes me off b, unless they are its last owner.
func (s *Service) leave(tx Tx, b *Board, me Member) error {
	if me.Access == rules.AccessAdmin {
		people, err := peopleOn(tx, *b)
		if err != nil {
			return err
		}
		if len(ownersOf(people)) == 1 {
			return apierr.New(http.StatusConflict, "last_owner",
				fmt.Sprintf("You're the last owner of board %s, so you can't leave it.", b.Name),
				"Make someone else an owner first: aboard board owner @name --board "+b.Name+".")
		}
	}
	_, err := s.takeOff(tx, b, me, StatusLeft, events.PersonLeft, actorOf(me), nil)
	return err
}

// takeOff ends a person's membership of b, and records why, with extra in the event's
// data. Their agents on b end with them, for good: each is marked removed, so its token
// never works on b again, even if the person comes back, and the event names them. The
// join codes the person and their agents made for b stop working too. It returns the
// agents that ended.
func (s *Service) takeOff(tx Tx, b *Board, m Member, status, typ string, actor events.Actor, extra map[string]any) ([]string, error) {
	now := s.clk.Now()
	if err := tx.SetMemberStatus(m.ID, status); err != nil {
		return nil, err
	}
	members, err := tx.Members(b.ID)
	if err != nil {
		return nil, err
	}
	// Who ended the agents: their own person leaving, a server admin removing the person
	// from the server, or one of the board's owners.
	by := RemovedByOwner
	switch {
	case typ == events.PersonLeft:
		by = RemovedByPerson
	case extra["from_server"] == true:
		by = RemovedByAdmin
	}
	if err := s.dropSeatTasks(tx, b, m.ID, actor, now); err != nil {
		return nil, err
	}
	theirs := map[string]bool{}
	agents := []string{}
	for _, x := range members {
		if x.HumanID != m.HumanID {
			continue
		}
		theirs[x.ID] = true
		if x.Status == StatusActive {
			if err := s.dropSeatTasks(tx, b, x.ID, actor, now); err != nil {
				return nil, err
			}
		}
		if x.Kind == "agent" && x.Status == StatusActive {
			if err := tx.RemoveAgent(x.ID, stamp(now), by); err != nil {
				return nil, err
			}
			agents = append(agents, x.ID)
		}
	}
	data := map[string]any{"member_id": m.ID, "person_id": m.HumanID, "name": m.Name, "agents": agents}
	for k, v := range extra {
		data[k] = v
	}
	if _, err := s.append(tx, b, typ, actor, now, data); err != nil {
		return nil, err
	}
	codes, err := tx.WorkingJoinCodes(b.ID, stamp(now))
	if err != nil {
		return nil, err
	}
	var stop []JoinCode
	for _, jc := range codes {
		if theirs[jc.CreatedBy] {
			stop = append(stop, jc)
		}
	}
	return agents, s.cancelCodes(tx, b, stop, actor, now)
}

// cancelCodes revokes join codes, recording each.
func (s *Service) cancelCodes(tx Tx, b *Board, codes []JoinCode, actor events.Actor, at time.Time) error {
	for _, jc := range codes {
		if err := tx.RevokeJoinCode(jc.ID, stamp(at)); err != nil {
			return err
		}
		if _, err := s.append(tx, b, events.JoinCodeRevoked, actor, at, map[string]any{"join_code_id": jc.ID}); err != nil {
			return err
		}
	}
	return nil
}

// MakeOwner makes a person on the board one of its owners. Only owners may. It reports
// false, recording nothing, when they already are one.
func (s *Service) MakeOwner(ctx context.Context, p Principal, boardName, handle string) (Person, bool, error) {
	if err := humanOnly(p, "make someone an owner of a board", "aboard board owner @"+handle+" --board "+boardName); err != nil {
		return Person{}, false, err
	}
	var out Person
	var b Board
	changed := false
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var me Member
		var err error
		if b, me, err = s.access(tx, p, boardName); err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if err := requireOwner(tx, b, me, "make someone an owner"); err != nil {
			return err
		}
		if out, err = onBoardByHandle(tx, b, handle); err != nil {
			return err
		}
		if out.Person.Role == ServerGuest {
			return personIsGuest(handle, "A guest never owns a board; make someone on the server an owner instead.")
		}
		if out.IsOwner() {
			return nil
		}
		if err := tx.SetMemberAccess(out.Member.ID, rules.AccessAdmin); err != nil {
			return err
		}
		out.Member.Access, changed = rules.AccessAdmin, true
		_, err = s.append(tx, &b, events.PersonMadeOwner, actorOf(me), s.clk.Now(), map[string]any{
			"member_id": out.Member.ID, "person_id": out.Person.ID, "name": out.Member.Name,
		})
		return err
	})
	if err != nil {
		return Person{}, false, err
	}
	if changed {
		s.notify.Changed(b.ID)
	}
	return out, changed, nil
}

// Reveals is what turning a private board open shows every person on the server.
type Reveals struct {
	Messages int64
	Files    int64
}

// errPreview ends the transaction of a previewed change, so nothing it did is kept.
var errPreview = errors.New("preview only")

// VisibilityChange is a board turning open or private, made or previewed.
type VisibilityChange struct {
	Board         string
	Before, After string
	Changed       bool
	DryRun        bool
	Reveals       *Reveals // set for private to open
	CodesCanceled int
}

// SetVisibility turns a board open or private. Only its owners may. Turning it private
// keeps the people on it, with their agents, and stops every join code that still works;
// turning it open lets every person on the server see it and join it, and so read its
// whole history, which Reveals counts. With dryRun nothing changes and the result says
// what would.
func (s *Service) SetVisibility(ctx context.Context, p Principal, boardName, visibility string, dryRun bool) (VisibilityChange, error) {
	if err := humanOnly(p, "turn a board open or private", "aboard board visibility "+visibility+" --board "+boardName); err != nil {
		return VisibilityChange{}, err
	}
	if visibility != BoardOpen && visibility != BoardPrivate {
		return VisibilityChange{}, invalid(fmt.Sprintf("%q is not a board visibility.", visibility), "Use open or private.")
	}
	var out VisibilityChange
	var b Board
	// write ends a preview with errPreview, before anything is written, so its
	// transaction rolls back.
	write := func(tx Tx) error {
		b2, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		b = b2
		if visibility == BoardOpen {
			if err := requireActive(b); err != nil {
				return err
			}
		}
		if err := requireOwner(tx, b, me, "turn it open or private"); err != nil {
			return err
		}
		out = VisibilityChange{Board: b.Name, Before: b.Visibility, After: visibility, DryRun: dryRun}
		if b.Visibility == visibility {
			return nil
		}
		out.Changed = true
		now := s.clk.Now()
		var codes []JoinCode
		if visibility == BoardOpen {
			// Files aren't stored on boards yet, so none are revealed.
			out.Reveals = &Reveals{Messages: b.MessageCount}
		} else {
			if codes, err = tx.WorkingJoinCodes(b.ID, stamp(now)); err != nil {
				return err
			}
			out.CodesCanceled = len(codes)
		}
		if dryRun {
			return errPreview
		}
		var reveals any
		if out.Reveals != nil {
			reveals = map[string]int64{"messages": out.Reveals.Messages, "files": out.Reveals.Files}
		}
		if visibility == BoardPrivate {
			b.AgentsAddPeople = false
			if err := tx.SetBoardAgentsAddPeople(b.ID, false); err != nil {
				return err
			}
		}
		if _, err := s.append(tx, &b, events.BoardVisibilityChanged, actorOf(me), now, map[string]any{
			"before": b.Visibility, "after": visibility, "reveals": reveals, "agents_add_people": b.AgentsAddPeople,
		}); err != nil {
			return err
		}
		if err := tx.SetBoardVisibility(b.ID, visibility); err != nil {
			return err
		}
		return s.cancelCodes(tx, &b, codes, actorOf(me), now)
	}
	// A preview runs the same checks in a write transaction it then rolls back.
	if err := s.writeAs(ctx, p, write); err != nil && !errors.Is(err, errPreview) {
		return VisibilityChange{}, err
	}
	if out.Changed && !dryRun {
		s.notify.Changed(b.ID)
	}
	return out, nil
}

// Settings are the server's settings.
type Settings struct {
	AgentsAddPeople *bool
	// BoardCreation is who may create boards: CreationMembers or CreationAdmins.
	BoardCreation string
}

// ServerSettings returns the server's settings, to any person.
func (s *Service) ServerSettings(ctx context.Context, p Principal) (Settings, error) {
	if err := requireHuman(p); err != nil {
		return Settings{}, err
	}
	var out Settings
	err := s.st.Read(ctx, func(tx ReadTx) error {
		var err error
		out.BoardCreation, err = tx.BoardCreation()
		if err != nil {
			return err
		}
		allowed, err := tx.AgentsAddPeople()
		out.AgentsAddPeople = &allowed
		return err
	})
	return out, err
}

// UpdateServerSettings changes the server's settings. Only a server admin may, with
// their own access key: not an agent, and not a browser.
func (s *Service) UpdateServerSettings(ctx context.Context, p Principal, change Settings) (Settings, error) {
	if err := requireHuman(p); err != nil {
		return Settings{}, err
	}
	if p.Browser {
		return Settings{}, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't change the server's settings; only your own access key can.",
			"Run the change in a terminal with your access key.")
	}
	if change.BoardCreation != "" && change.BoardCreation != CreationMembers && change.BoardCreation != CreationAdmins {
		return Settings{}, invalid(fmt.Sprintf("%q is not a board creation setting.", change.BoardCreation), "Use members or admins.")
	}
	var out Settings
	err := s.writeAs(ctx, p, func(tx Tx) error {
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		if h.Role != ServerAdmin {
			return apierr.New(http.StatusForbidden, "server_admin_required",
				"Only an admin of this server can change its settings.",
				"Ask an admin of this server to change it.")
		}
		if change.BoardCreation != "" {
			if err := tx.SetBoardCreation(change.BoardCreation); err != nil {
				return err
			}
		}
		if change.AgentsAddPeople != nil {
			if err := tx.SetAgentsAddPeople(*change.AgentsAddPeople); err != nil {
				return err
			}
		}
		out.BoardCreation, err = tx.BoardCreation()
		if err != nil {
			return err
		}
		allowed, err := tx.AgentsAddPeople()
		out.AgentsAddPeople = &allowed
		return err
	})
	return out, err
}

// admitNewPerson appends the ordinary admission and read model together. Its caller
// must check the issuing person's current authority in the same transaction.
func (s *Service) admitNewPerson(tx Tx, b *Board, me Member, target Human, on bool, provenance map[string]any) (Person, error) {
	now := s.clk.Now()
	var err error
	m := Member{
		BoardID: b.ID, Kind: "human", HumanID: target.ID, Access: rules.AccessMember, Status: StatusActive, JoinedAt: stamp(now),
		Name: rules.AllocateName(target.Name, func(n string) bool { _, err := tx.MemberByName(b.ID, n); return err == nil }),
	}
	if m.ID, err = s.gen.ID("mem", now); err != nil {
		return Person{}, err
	}
	if err := tx.InsertMember(m); err != nil {
		return Person{}, fmt.Errorf("insert member: %w", err)
	}
	actor := actorOf(me)
	if !on {
		actor = actorOf(m)
	}
	data := map[string]any{"member_id": m.ID, "person_id": target.ID, "name": m.Name, "access": m.Access, "rejoined": false}
	for key, value := range provenance {
		data[key] = value
	}
	if _, err := s.append(tx, b, events.PersonAdded, actor, now, data); err != nil {
		return Person{}, err
	}
	if err := startReading(tx, *b, m); err != nil {
		return Person{}, err
	}
	return Person{Member: m, Person: target}, nil
}
