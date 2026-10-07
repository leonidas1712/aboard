package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/joinline"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Join codes come in two kinds. A pairing code lets in only its maker's own sessions: the
// person who made it, or whose agent made it, redeeming it with their own access key. A
// guest code lets one person from outside the server onto its board, once, as the guest
// it names; only a person on the board makes one, and it is redeemed with no credential
// at all (or, by a guest who has a key already, with that key), so it is the only code
// that brings anyone new onto a board.

// DefaultJoinCodeTTL is how long a join code works when no lifetime is given.
const DefaultJoinCodeTTL = 24 * time.Hour

// guestLineRole is what a guest code's join line says in place of a role.
const guestLineRole = "guest"

// NewJoinCode is a created join code. Code and Line are only available at creation.
type NewJoinCode struct {
	JoinCode JoinCode
	Board    string
	Creator  Member
	Code     string
	Line     string
}

// JoinCodeInput is a request for a join code: the role its agents join in, how long it
// works (DefaultJoinCodeTTL when zero), and for a guest code the guest's handle.
type JoinCodeInput struct {
	Role  string
	TTL   time.Duration
	Guest string
}

func roleNotFound(role string) *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, "role_not_found",
		fmt.Sprintf("This board has no role %q.", role), "Use a role defined in the board's charter and roles.")
}

func joinCodeInvalid() *apierr.Error {
	return apierr.New(http.StatusNotFound, "join_code_invalid",
		"That join code doesn't work: it is wrong, expired or revoked.", "Ask for a new join line from someone on the board.")
}

// CreateJoinCode makes a pairing code, or with in.Guest a guest code, for the board.
func (s *Service) CreateJoinCode(ctx context.Context, p Principal, boardName string, in JoinCodeInput) (NewJoinCode, error) {
	if in.Guest != "" {
		if err := humanOnly(p, "make a guest code", "aboard invite --guest "+in.Guest+" --board "+boardName); err != nil {
			return NewJoinCode{}, err
		}
		if !validName(in.Guest) {
			return NewJoinCode{}, apierr.New(http.StatusUnprocessableEntity, "handle_invalid",
				fmt.Sprintf("%q can't be a handle: use lowercase letters, digits and single dashes, at most 40 characters.", in.Guest),
				"Pick a handle such as "+rules.NormalizeName(in.Guest)+".")
		}
	}
	ttl := in.TTL
	if ttl == 0 {
		ttl = DefaultJoinCodeTTL
	}
	var out NewJoinCode
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if me.PersonRole == ServerGuest {
			return guestNotAllowed("make join codes")
		}
		if p.Agent != nil && !b.Roles[*me.Role].Has(rules.Invite) {
			return apierr.New(http.StatusForbidden, "forbidden", "Your role can't invite new agents to this board.",
				"Ask a human on the board to create the join code.")
		}
		if _, ok := b.Roles[in.Role]; !ok {
			return roleNotFound(in.Role)
		}
		if in.Guest != "" {
			if err := guestMayCome(tx, b, in.Guest); err != nil {
				return err
			}
		}
		now := s.clk.Now()
		id, err := s.gen.ID("jc", now)
		if err != nil {
			return err
		}
		code, err := s.gen.JoinCode()
		if err != nil {
			return err
		}
		jc := JoinCode{
			ID: id, BoardID: b.ID, CodeDigest: ids.Digest(s.key, code), Role: in.Role,
			ExpiresAt: stamp(now.Add(ttl)), CreatedAt: stamp(now), CreatedBy: me.ID, Kind: CodePairing,
		}
		data := map[string]any{"join_code_id": jc.ID, "role": jc.Role, "expires_at": jc.ExpiresAt}
		lineRole := in.Role
		if in.Guest != "" {
			jc.Kind, jc.Guest = CodeGuest, ptr(in.Guest)
			guest, err := tx.HumanByName(in.Guest)
			if err == nil {
				jc.GuestID = ptr(guest.ID)
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			data["kind"], data["guest"], data["guest_id"] = CodeGuest, in.Guest, jc.GuestID
			lineRole = guestLineRole
		}
		if err := tx.InsertJoinCode(jc); err != nil {
			return fmt.Errorf("insert join code: %w", err)
		}
		if _, err := s.append(tx, &b, events.JoinCodeCreated, actorOf(me), now, data); err != nil {
			return err
		}
		out = NewJoinCode{
			JoinCode: jc, Board: b.Name, Creator: me, Code: code,
			Line: joinline.Line{Board: b.Name, Server: s.cfg.JoinHost, Role: lineRole, Code: code}.String(),
		}
		return nil
	})
	return out, err
}

// guestMayCome refuses a guest code for handle on b when the handle is a member's or an
// admin's of the server, or a guest's who is already on b.
func guestMayCome(tx ReadTx, b Board, handle string) error {
	h, err := tx.HumanByName(handle)
	if errors.Is(err, ErrNotFound) {
		if _, reserved := tx.ReservedHandle(handle); reserved == nil {
			return HandleTaken(handle)
		} else if !errors.Is(reserved, ErrNotFound) {
			return reserved
		}
		return nil
	}
	if err != nil {
		return err
	}
	if h.Role != ServerGuest {
		return apierr.New(http.StatusConflict, "handle_taken",
			fmt.Sprintf("%s is already on this server, so they don't need a guest code.", handle),
			fmt.Sprintf("Add them to the board instead: aboard board add @%s --board %s.", handle, b.Name))
	}
	m, err := tx.HumanMember(b.ID, h.ID)
	if err == nil && m.Status == StatusActive {
		return apierr.New(http.StatusConflict, "already_on_board",
			fmt.Sprintf("%s is already on board %s.", handle, b.Name),
			"Run aboard board people --board "+b.Name+" to see who is.")
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// RevokeJoinCode stops a join code from working. Agents that already joined stay.
func (s *Service) RevokeJoinCode(ctx context.Context, p Principal, boardName, id string) (JoinCode, Member, error) {
	var jc JoinCode
	var creator Member
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		if me.PersonRole == ServerGuest {
			return guestNotAllowed("cancel join codes")
		}
		jc, err = tx.JoinCodeByID(id)
		if errors.Is(err, ErrNotFound) || (err == nil && jc.BoardID != b.ID) {
			return apierr.New(http.StatusNotFound, "join_code_not_found", "There is no such join code on this board.",
				"Check the join code id.")
		}
		if err != nil {
			return err
		}
		if p.Agent != nil && jc.CreatedBy != me.ID {
			return apierr.New(http.StatusForbidden, "forbidden", "Only a human or the agent that created a join code can revoke it.",
				"Ask a human on the board to revoke it.")
		}
		if jc.RevokedAt == nil {
			now := s.clk.Now()
			if err := tx.RevokeJoinCode(jc.ID, stamp(now)); err != nil {
				return err
			}
			if _, err := s.append(tx, &b, events.JoinCodeRevoked, actorOf(me), now, map[string]any{"join_code_id": jc.ID}); err != nil {
				return err
			}
			jc.RevokedAt = ptr(stamp(now))
		}
		creator, err = memberByID(tx, b.ID, jc.CreatedBy)
		return err
	})
	return jc, creator, err
}

// memberByID finds a member of board boardID by id.
func memberByID(tx ReadTx, boardID, id string) (Member, error) {
	members, err := tx.Members(boardID)
	if err != nil {
		return Member{}, err
	}
	for _, m := range members {
		if m.ID == id {
			return m, nil
		}
	}
	return Member{}, ErrNotFound
}

// JoinInput is a request for a new agent identity: either Code, or Board and Role from
// a human who is already a member. Session is the harness session the agent is for
// (`<harness>:<id>`), when the caller names one; it is kept with the seat.
type JoinInput struct {
	seatID  string
	Code    string
	Board   string
	Role    string
	Name    string
	Harness string
	Session string
}

// Joined is an agent with its token, which is only available here: a new one, or, with
// Reused, a session's working seat that a delegated join gave a new token.
type Joined struct {
	Agent  Member
	Token  string
	View   View
	Reused bool
}

// workingCode finds the join code code names, and fails with join_code_invalid unless it
// still works at now: not revoked, not expired and, for a guest code, not used.
func (s *Service) workingCode(tx ReadTx, code, now string) (JoinCode, error) {
	normal, ok := ids.NormalizeJoinCode(code)
	if !ok {
		return JoinCode{}, joinCodeInvalid()
	}
	jc, err := tx.JoinCodeByDigest(ids.Digest(s.key, normal))
	if errors.Is(err, ErrNotFound) {
		return JoinCode{}, joinCodeInvalid()
	}
	if err != nil {
		return JoinCode{}, err
	}
	if jc.RevokedAt != nil || jc.UsedAt != nil || jc.ExpiresAt <= now {
		return JoinCode{}, joinCodeInvalid()
	}
	if _, err := codeMaker(tx, jc); err != nil {
		return JoinCode{}, err
	}
	b, err := tx.BoardByID(jc.BoardID)
	if err != nil {
		return JoinCode{}, err
	}
	if lifecycleOf(b) == LifecycleDeleted {
		return JoinCode{}, joinCodeInvalid()
	}
	return jc, nil
}

// codeMaker returns the person who made jc, or whose agent made it, as long as that
// member and their person are still on jc's board; otherwise the code has stopped
// working, and it fails with join_code_invalid.
func codeMaker(tx ReadTx, jc JoinCode) (Member, error) {
	maker, err := memberByID(tx, jc.BoardID, jc.CreatedBy)
	if errors.Is(err, ErrNotFound) {
		return Member{}, joinCodeInvalid()
	}
	if err != nil {
		return Member{}, err
	}
	person, err := tx.HumanMember(jc.BoardID, maker.HumanID)
	if errors.Is(err, ErrNotFound) || (err == nil && (person.Status != StatusActive || maker.Status != StatusActive)) {
		return Member{}, joinCodeInvalid()
	}
	if err != nil {
		return Member{}, err
	}
	return person, nil
}

// Join creates a new agent owned by the calling person, who must be on the board: with
// a pairing code they or their agent made, or directly with a board and a role. A guest
// joins only with a guest code for them, which puts them on its board.
func (s *Service) Join(ctx context.Context, p Principal, in JoinInput) (Joined, error) {
	if p.Delegation != nil {
		return s.joinDelegated(ctx, p, in)
	}
	if err := requireHuman(p); err != nil {
		return Joined{}, err
	}
	var out Joined
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		person, err := caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		var b Board
		var role string
		var codeID *string
		switch {
		case in.Code != "":
			jc, err := s.workingCode(tx, in.Code, stamp(now))
			if err != nil {
				return err
			}
			if b, err = tx.BoardByID(jc.BoardID); err != nil {
				return err
			}
			if jc.Kind == CodeGuest {
				if person.Role != ServerGuest || (jc.GuestID != nil && *jc.GuestID != person.ID) || (jc.GuestID == nil && deref(jc.Guest) != person.Name) {
					return apierr.New(http.StatusForbidden, "guest_code_not_for_members",
						fmt.Sprintf("That is a guest code for %s: it brings them onto board %s from outside this server, and you are on this server already.", deref(jc.Guest), b.Name),
						fmt.Sprintf("Ask someone on %s to add you: aboard board add @%s --board %s.", b.Name, person.Name, b.Name))
				}
				if jc.GuestID == nil || *jc.GuestID != person.ID {
					return joinCodeInvalid()
				}
				out, err = s.redeemGuestCode(tx, jc, person, ptr(p.KeyID), JoinInput{Name: in.Name, Harness: in.Harness, Session: in.Session}, now)
				return err
			}
			if person.Role == ServerGuest {
				return guestNotAllowed("join with a pairing code")
			}
			maker, err := codeMaker(tx, jc)
			if err != nil {
				return err
			}
			if maker.HumanID != p.Human.ID {
				return apierr.New(http.StatusForbidden, "join_code_not_yours",
					fmt.Sprintf("That join code is for %s's own sessions on board %s.", maker.Name, b.Name),
					fmt.Sprintf("Ask someone on %s to add you (aboard board add @%s --board %s), then make your own join line with aboard invite --board %s.",
						b.Name, p.Human.Name, b.Name, b.Name))
			}
			role, codeID = jc.Role, ptr(jc.ID)
		case in.Board != "" && in.Role != "":
			if person.Role == ServerGuest {
				return guestNotAllowed("add agents to a board")
			}
			if b, _, err = s.access(tx, p, in.Board); err != nil {
				return err
			}
			if _, ok := b.Roles[in.Role]; !ok {
				return roleNotFound(in.Role)
			}
			role = in.Role
		default:
			return invalid("A join needs a code, or a board and a role.", "Paste the whole join line into aboard join.")
		}
		if err := requireActive(b); err != nil {
			return err
		}
		owner, err := tx.HumanMember(b.ID, p.Human.ID)
		if errors.Is(err, ErrNotFound) || (err == nil && owner.Status != StatusActive) {
			return joinCodeInvalid()
		}
		if err != nil {
			return err
		}
		// The agent's token stops working when the access key that made it does.
		var keyID *string
		if p.KeyID != "" {
			keyID = ptr(p.KeyID)
		}
		out, err = s.seat(tx, &b, owner, p.Human.Name, in, role, keyID, codeID, now, nil)
		return err
	})
	if err != nil {
		return Joined{}, err
	}
	s.notify.Changed(out.View.Board.ID)
	s.notify.Changed(boardsOfKey(p.Human.ID))
	return out, nil
}

// seat makes a new agent of owner's person on b, in role, and records it.
func (s *Service) seat(tx Tx, b *Board, owner Member, ownerName string, in JoinInput, role string, keyID, codeID *string, now time.Time, extra map[string]any) (Joined, error) {
	taken := func(n string) bool { _, err := tx.MemberByName(b.ID, n); return err == nil }
	name := in.Name
	switch {
	case name != "" && taken(name):
		return Joined{}, apierr.New(http.StatusConflict, "name_taken", fmt.Sprintf("Someone on this board is already called %q.", name),
			"Choose another name, or leave the name out to get a free one.")
	case name != "":
	case !b.Policy.ShowHarness:
		name = rules.AllocateNumberedName("agent", taken)
	default:
		name = rules.AllocateName(rules.AgentNameBase(in.Harness, role), taken)
	}
	token, err := s.gen.Token("aba")
	if err != nil {
		return Joined{}, err
	}
	agent := Member{
		BoardID: b.ID, Name: name, Kind: "agent", Role: ptr(role), HumanID: owner.HumanID, Owner: ptr(ownerName),
		TokenDigest: ptr(ids.Digest(s.key, token)), Status: StatusActive, JoinedAt: stamp(now), KeyID: keyID,
		// A new agent starts reading at the board's head: earlier messages are in the
		// timeline, not its inbox.
		Cursor: b.HeadSeq + 1, PersonRole: owner.PersonRole,
	}
	if in.Harness != "" {
		agent.Harness = ptr(in.Harness)
	}
	if in.Session != "" {
		agent.Session = ptr(in.Session)
	}
	agent.ID = in.seatID
	if agent.ID == "" {
		if agent.ID, err = s.gen.ID("mem", now); err != nil {
			return Joined{}, err
		}
	}
	if err := s.addMember(tx, b, agent, actorOf(owner), codeID, now, extra); err != nil {
		return Joined{}, err
	}
	view, err := viewOf(tx, *b)
	if err != nil {
		return Joined{}, err
	}
	view.OnBoard = true
	view.Brief, err = projectBrief(tx, *b, agent)
	if err != nil {
		return Joined{}, err
	}
	return Joined{Agent: agent, Token: token, View: view}, nil
}

// GuestJoinInput redeems a guest code: the code, the name of the key it gives the
// guest's machine, and the new agent's name and harness.
type GuestJoinInput struct {
	Code    string
	KeyName string
	Name    string
	Harness string
}

// GuestJoined is a guest on a board, with their machine's new access key and their new
// agent. The key's and the agent's secrets are only available here.
type GuestJoined struct {
	Joined
	Person   Human
	Key      AccessKey
	KeyToken string
}

// GuestJoin uses up a guest code and, in the same transaction, puts the guest it names
// onto its board, as a newly created person with the server role guest, with
// an access key for their machine and a new agent, whose secrets are returned. A code
// that is wrong, used, expired, revoked, a pairing code, or whose maker has left the
// board or the server fails the same way.
func (s *Service) GuestJoin(ctx context.Context, in GuestJoinInput) (GuestJoined, error) {
	if !validName(in.KeyName) {
		return GuestJoined{}, invalid("A key's name uses lowercase letters, digits and single dashes, at most 40 characters.",
			"Name the key after the machine that keeps it, such as sam-laptop.")
	}
	var out GuestJoined
	err := s.st.Write(ctx, func(tx Tx) error {
		now := s.clk.Now()
		jc, err := s.workingCode(tx, in.Code, stamp(now))
		if err != nil {
			return err
		}
		if jc.Kind != CodeGuest || jc.Guest == nil || jc.GuestID != nil {
			return joinCodeInvalid()
		}
		guest, err := s.guestPerson(tx, *jc.Guest, now)
		if err != nil {
			return err
		}
		token, key, err := s.newKey(tx, guest.ID, in.KeyName, now, nil, ptr(MachineKeyIdle))
		if err != nil {
			return err
		}
		joined, err := s.redeemGuestCode(tx, jc, guest, ptr(key.ID), JoinInput{Name: in.Name, Harness: in.Harness}, now)
		out = GuestJoined{Joined: joined, Person: guest, Key: key, KeyToken: token}
		return err
	})
	if err != nil {
		return GuestJoined{}, err
	}
	s.notify.Changed(out.View.Board.ID)
	s.notify.Changed(boardsOfKey(out.Person.ID))
	return out, nil
}

// redeemGuestCode uses up guest code jc for guest, putting them on its board with a new
// agent whose token stops with keyID. It checks again, as the code is used, that its
// maker is still on the board and on the server.
func (s *Service) redeemGuestCode(tx Tx, jc JoinCode, guest Human, keyID *string, in JoinInput, now time.Time) (Joined, error) {
	maker, err := codeMaker(tx, jc)
	if err != nil {
		return Joined{}, err
	}
	if h, err := tx.HumanByID(maker.HumanID); err != nil || h.RemovedAt != nil || h.Role == ServerGuest {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Joined{}, err
		}
		return Joined{}, joinCodeInvalid()
	}
	b, err := tx.BoardByID(jc.BoardID)
	if err != nil {
		return Joined{}, err
	}
	if err := requireActive(b); err != nil {
		return Joined{}, err
	}
	me, err := s.guestOnBoard(tx, &b, guest, ptr(jc.ID), now)
	if err != nil {
		return Joined{}, err
	}
	out, err := s.seat(tx, &b, me, guest.Name, in, jc.Role, keyID, ptr(jc.ID), now, nil)
	if err != nil {
		return Joined{}, err
	}
	if used, err := tx.UseJoinCode(jc.ID, stamp(now), out.Agent.ID); err != nil || !used {
		if err != nil {
			return Joined{}, err
		}
		return Joined{}, joinCodeInvalid()
	}
	return out, nil
}

// guestPerson creates a new guest. A code never proves an existing person's identity,
// even if its handle was free when issued.
func (s *Service) guestPerson(tx Tx, handle string, now time.Time) (Human, error) {
	if _, err := tx.ReservedHandle(handle); err == nil {
		return Human{}, joinCodeInvalid()
	} else if !errors.Is(err, ErrNotFound) {
		return Human{}, err
	}
	h, err := tx.HumanByName(handle)
	switch {
	case err == nil:
		return Human{}, joinCodeInvalid()
	case !errors.Is(err, ErrNotFound):
		return Human{}, err
	}
	h = Human{Name: handle, Role: ServerGuest, CreatedAt: stamp(now)}
	if h.ID, err = s.gen.ID("hum", now); err != nil {
		return Human{}, err
	}
	if err := tx.InsertHuman(h); err != nil {
		return Human{}, fmt.Errorf("insert guest: %w", err)
	}
	return h, nil
}

// guestOnBoard puts a guest on b, as a member, unless they are on it already, and
// returns their membership.
func (s *Service) guestOnBoard(tx Tx, b *Board, guest Human, codeID *string, now time.Time) (Member, error) {
	m, err := tx.HumanMember(b.ID, guest.ID)
	switch {
	case err == nil && m.Status == StatusActive:
		return m, nil
	case err == nil:
		err := s.restorePerson(tx, b, &m, actorOf(m), now, nil)
		return m, err
	case !errors.Is(err, ErrNotFound):
		return Member{}, err
	}
	m = Member{
		BoardID: b.ID, Kind: "human", HumanID: guest.ID, Access: rules.AccessMember, Status: StatusActive, JoinedAt: stamp(now),
		Name:       rules.AllocateName(guest.Name, func(n string) bool { _, err := tx.MemberByName(b.ID, n); return err == nil }),
		PersonRole: ServerGuest,
	}
	if m.ID, err = s.gen.ID("mem", now); err != nil {
		return Member{}, err
	}
	if err := tx.InsertMember(m); err != nil {
		return Member{}, fmt.Errorf("insert member: %w", err)
	}
	_, err = s.append(tx, b, events.MemberJoined, actorOf(m), now, map[string]any{
		"member_id": m.ID, "name": m.Name, "kind": m.Kind, "role": nil, "owner": nil,
		"harness": nil, "access": m.Access, "join_code_id": codeID, "guest": true,
	})
	if err != nil {
		return Member{}, err
	}
	if err := startReading(tx, *b, m); err != nil {
		return Member{}, err
	}
	return m, nil
}
