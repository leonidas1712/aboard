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
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// A machine's delegation lets the program that runs a person's agents on a machine (the
// delivery daemon) find and join that person's boards for the sessions it runs, without
// any agent holding the person's key. It is made with one of the person's own access
// keys and acts within the person's current access: list the boards they can see,
// give a vouched session a seat, or create a board with that session's seat. It ends with
// its key, with its person's removal from the server, and when the same key makes
// another with the same name.

// delegationPrefix starts every delegation's token.
const delegationPrefix = "abd_"

func delegationRevoked() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "delegation_revoked",
		"This machine's delegation no longer works: the access key it came from was revoked or expired, or its person is no longer on the server.",
		"Your person runs aboard login or aboard connect on this machine, then you run the command again.")
}

// delegationForbidden refuses operations outside the delegation's explicit scope.
func delegationForbidden() *apierr.Error {
	return apierr.New(http.StatusForbidden, "forbidden",
		"A machine's delegation only lists its person's boards, joins sessions and creates boards with session seats.",
		"Use the person's own access key or the agent's token for anything else.")
}

// asDelegate returns p allowed to act as its delegation; only the operations a
// delegation may do call it.
func (p Principal) asDelegate() Principal {
	p.delegated = true
	return p
}

// checkDelegation checks, with the time read inside the caller's transaction, that a
// delegation, the access key it came from and its person all still work, and returns
// the person and when the key expires.
func checkDelegation(tx ReadTx, d Delegation, now string) (Human, *string, error) {
	cur, err := tx.DelegationByID(d.ID)
	if errors.Is(err, ErrNotFound) {
		return Human{}, nil, delegationRevoked()
	}
	if err != nil {
		return Human{}, nil, err
	}
	if cur.EndedAt != nil {
		return Human{}, nil, delegationRevoked()
	}
	k, err := tx.AccessKeyByID(cur.KeyID)
	if errors.Is(err, ErrNotFound) {
		return Human{}, nil, delegationRevoked()
	}
	if err != nil {
		return Human{}, nil, err
	}
	if !keyWorks(k, now) || k.HumanID != cur.HumanID {
		return Human{}, nil, delegationRevoked()
	}
	person, err := tx.HumanByID(cur.HumanID)
	if errors.Is(err, ErrNotFound) || (err == nil && person.RemovedAt != nil) {
		return Human{}, nil, delegationRevoked()
	}
	if err != nil {
		return Human{}, nil, err
	}
	return person, k.ExpiresAt, nil
}

// authenticateDelegation resolves a delegation's token. A token that was never issued is
// unauthorized; one that no longer works is delegation_revoked.
func (s *Service) authenticateDelegation(ctx context.Context, token string) (Principal, error) {
	var d Delegation
	var key AccessKey
	err := s.st.Read(ctx, func(tx ReadTx) error {
		var err error
		if d, err = tx.DelegationByDigest(ids.Digest(s.key, token)); err != nil {
			return err
		}
		if _, _, err = checkDelegation(tx, d, stamp(s.clk.Now())); err != nil {
			return err
		}
		key, err = tx.AccessKeyByID(d.KeyID)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return Principal{}, apierr.Unauthorized()
	}
	if err != nil {
		if _, ok := apierr.As(err); ok {
			return Principal{}, err
		}
		return Principal{}, fmt.Errorf("authenticate delegation: %w", err)
	}
	p := Principal{Delegation: &d, KeyID: d.KeyID}
	// Its use counts as a use of its key, as an agent token's does.
	if err := s.recordUse(ctx, p.asDelegate(), key); err != nil {
		return Principal{}, err
	}
	return p, nil
}

// NewDelegation is a made delegation. Token, its secret, exists only here.
type NewDelegation struct {
	Delegation Delegation
	Token      string
}

// CreateDelegation makes a machine's delegation, named name, with the caller's own access
// key, ending the key's earlier delegation with the same name in the same transaction.
// Only a person's own key can: never an agent, a browser or another delegation. A guest
// may; their delegation lists only their boards.
func (s *Service) CreateDelegation(ctx context.Context, p Principal, name string) (NewDelegation, error) {
	if p.Delegation != nil || p.Human == nil || p.Browser {
		return NewDelegation{}, apierr.New(http.StatusForbidden, "human_token_required",
			"Only a person's own access key can make a machine's delegation.",
			"The delivery daemon makes it with the key this machine keeps for the server; run aboard login if it has none.")
	}
	if p.KeyID == "" {
		return NewDelegation{}, apierr.Unauthorized()
	}
	if !validName(name) {
		return NewDelegation{}, invalid("A delegation's name uses lowercase letters, digits and single dashes, at most 40 characters.",
			"Name it after the machine that holds it, such as maya-laptop.")
	}
	var out NewDelegation
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		if err := tx.EndDelegations(p.KeyID, name, stamp(now)); err != nil {
			return fmt.Errorf("end earlier delegations: %w", err)
		}
		token, err := s.gen.Token(strings.TrimSuffix(delegationPrefix, "_"))
		if err != nil {
			return err
		}
		d := Delegation{KeyID: p.KeyID, HumanID: p.Human.ID, Name: name, Digest: ids.Digest(s.key, token), CreatedAt: stamp(now)}
		if d.ID, err = s.gen.ID("dlg", now); err != nil {
			return err
		}
		if err := tx.InsertDelegation(d); err != nil {
			return fmt.Errorf("insert delegation: %w", err)
		}
		out = NewDelegation{Delegation: d, Token: token}
		return nil
	})
	return out, err
}

// delegatedBoards lists, for a delegation, every board its person can see: the open
// boards and the private boards they are on, and for a guest only the boards they are
// on. It never lists hidden boards, and never the person's own read position.
func (s *Service) delegatedBoards(ctx context.Context, p Principal, filter string) (Listing, error) {
	p = p.asDelegate()
	var out Listing
	err := s.st.Read(ctx, func(tx ReadTx) (err error) {
		defer func() {
			if err == nil {
				err = s.filterListing(tx, p, &out, filter)
			}
		}()
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		var boards []Board
		if person.Role == ServerGuest {
			boards, err = tx.BoardsOfHuman(person.ID)
		} else {
			boards, err = tx.BoardsSeenBy(person.ID)
		}
		if err != nil {
			return err
		}
		for _, b := range boards {
			v, err := viewOf(tx, b)
			if err != nil {
				return err
			}
			members, err := tx.Members(b.ID)
			if err != nil {
				return err
			}
			people, agents := 0, 0
			v.OnBoard = false
			for _, m := range present(members) {
				switch {
				case m.Kind != "human":
					agents++
				case m.HumanID == person.ID:
					people++
					v.OnBoard = true
					if v.Added, err = addedBy(tx, b, m, members); err != nil {
						return err
					}
				default:
					people++
				}
			}
			v.PeopleCount = ptr(people)
			if v.OnBoard {
				v.AgentCount = ptr(agents)
			}
			out.Boards = append(out.Boards, v)
		}
		return nil
	})
	return out, err
}

func agentRemoved(seat Member, board string) *apierr.Error {
	e := apierr.New(http.StatusForbidden, "agent_removed",
		fmt.Sprintf("This session's agent %s on board %s was removed, and joining again never makes a replacement.", seat.Name, board),
		"A new agent on that board needs its person to allow it: they run aboard join --board "+board+" in a terminal, or add an agent in the board view.")
	e.Details = map[string]any{"agent": seat.Name, "board": board, "removed_at": seat.RemovedAt, "removed_by": seat.RemovedBy}
	return e
}

// joinDelegated gives the session a delegation vouches for a seat on a board its person
// can see. Everything is checked in the one transaction that writes, in this order, and
// a refusal at any step writes nothing: the delegation, its key and its person; that
// the person can see the board; the newest seat recorded for this person, board and
// session, which is reused with a new token unless it was removed; the person's place on
// an open board they aren't on, added as a member; and the board's roles and names.
func (s *Service) joinDelegated(ctx context.Context, p Principal, in JoinInput) (Joined, error) {
	p = p.asDelegate()
	d := *p.Delegation
	if in.Code != "" {
		return Joined{}, apierr.New(http.StatusForbidden, "forbidden",
			"A machine's delegation joins boards by name, never with a code.",
			"Join with aboard join --board NAME from the session.")
	}
	if in.Board == "" || in.Session == "" {
		return Joined{}, invalid("A delegated join needs the board and the session it is for.",
			"Send board and session (<harness>:<id>).")
	}
	role := in.Role
	if role == "" {
		role = rules.MemberRole
	}
	var out Joined
	var person Human
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		// 1. The delegation, its key and its person still work.
		var err error
		if person, err = caller(tx, p, stamp(now)); err != nil {
			return err
		}
		// 2. The person can see the board.
		byID := strings.HasPrefix(in.Board, "brd_")
		var b Board
		if byID {
			b, err = tx.BoardByID(in.Board)
		} else {
			b, err = tx.BoardByName(in.Board)
		}
		if errors.Is(err, ErrNotFound) {
			return apierr.BoardNotFound(in.Board)
		}
		if err != nil {
			return err
		}
		me, err := tx.HumanMember(b.ID, person.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		on := err == nil && me.Status == StatusActive
		if byID && !on {
			return apierr.BoardNotFound(in.Board)
		}
		switch {
		case person.Role == ServerGuest && on:
			return guestNotAllowed("add agents through a machine's delegation; a guest's agents come only from guest codes")
		case person.Role == ServerGuest, !on && b.Visibility != BoardOpen:
			return apierr.BoardNotFound(in.Board)
		}
		if lifecycleOf(b) == LifecycleDeleted {
			return apierr.BoardNotFound(in.Board)
		}
		if err := requireActive(b); err != nil {
			return err
		}
		// 3. The session's seat on the board, if it has one.
		seat, err := tx.SeatForSession(b.ID, person.ID, in.Session)
		switch {
		case err == nil && seat.Status != StatusActive:
			return agentRemoved(seat, b.Name)
		case err == nil && on:
			token, err := s.gen.Token("aba")
			if err != nil {
				return err
			}
			if err := tx.SetAgentToken(seat.ID, ids.Digest(s.key, token), d.KeyID); err != nil {
				return fmt.Errorf("give seat %s a new token: %w", seat.ID, err)
			}
			seat.TokenDigest, seat.KeyID = ptr(ids.Digest(s.key, token)), ptr(d.KeyID)
			view, err := viewOf(tx, b)
			if err != nil {
				return err
			}
			view.OnBoard = true
			view.Brief, err = projectBrief(tx, b, seat)
			if err != nil {
				return err
			}
			out = Joined{Agent: seat, Token: token, View: view, Reused: true}
			return nil
		case err != nil && !errors.Is(err, ErrNotFound):
			return err
		}
		via := map[string]any{"via": "delegation", "delegation_id": d.ID}
		// 4. The person is on the board: on an open board they aren't on, they are
		// added first, as a member.
		if !on {
			if me, err = s.addSelf(tx, &b, person, me, via, now); err != nil {
				return err
			}
		}
		// 5. The board's roles and names.
		if _, ok := b.Roles[role]; !ok {
			return roleNotFound(role)
		}
		out, err = s.seat(tx, &b, me, person.Name, in, role, ptr(d.KeyID), nil, now, via)
		return err
	})
	if err != nil {
		return Joined{}, err
	}
	if out.Reused {
		// Long reads made with the seat's earlier token check again, and end.
		s.notify.Changed(credentialsKey(person.ID))
	}
	s.notify.Changed(out.View.Board.ID)
	s.notify.Changed(boardsOfKey(person.ID))
	return out, nil
}

// addSelf puts person onto open board b as a member, as joining it themselves would:
// under their old member id when they were on it before (me), recording person.added
// with extra. The actor is the person.
func (s *Service) addSelf(tx Tx, b *Board, person Human, me Member, extra map[string]any, now time.Time) (Member, error) {
	if me.ID != "" {
		err := s.restorePerson(tx, b, &me, actorOf(me), now, extra)
		return me, err
	}
	m := Member{
		BoardID: b.ID, Kind: "human", HumanID: person.ID, Access: rules.AccessMember, Status: StatusActive, JoinedAt: stamp(now),
		Name:       rules.AllocateName(person.Name, func(n string) bool { _, err := tx.MemberByName(b.ID, n); return err == nil }),
		PersonRole: person.Role,
	}
	var err error
	if m.ID, err = s.gen.ID("mem", now); err != nil {
		return Member{}, err
	}
	if err := tx.InsertMember(m); err != nil {
		return Member{}, fmt.Errorf("insert member: %w", err)
	}
	data := map[string]any{"member_id": m.ID, "person_id": person.ID, "name": m.Name, "access": m.Access, "rejoined": false}
	for k, v := range extra {
		data[k] = v
	}
	if _, err := s.append(tx, b, events.PersonAdded, actorOf(m), now, data); err != nil {
		return Member{}, err
	}
	return m, startReading(tx, *b, m)
}

func seatTokenReplaced(board string) *apierr.Error {
	return apierr.New(http.StatusConflict, "seat_token_replaced",
		"This agent's token was replaced: a later join from the same session gave the seat a new one.",
		"Join again from that session: aboard join --board "+board+".")
}

// CheckJoinReplay decides whether the answer stored for a join with a person's key or a
// code may be returned again for a repeat with the same Idempotency-Key. It rechecks, in
// one read, that the caller's credential still works, that they still have the board
// (for a code, that its maker's authority still holds, even though the first call used
// it up), that the stored seat wasn't removed, and that its stored token still works.
// The board is checked first, so a replay never says anything about a board the caller
// can no longer see; on one they still see, a removed seat is agent_removed.
// A failed check is the error a new call would get now; a token a later delegated join
// replaced is seat_token_replaced.
func (s *Service) CheckJoinReplay(ctx context.Context, p Principal, in JoinInput, memberID, token string) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		now := stamp(s.clk.Now())
		person, err := caller(tx, p, now)
		if err != nil {
			return err
		}
		seat, err := tx.MemberByID(memberID)
		if errors.Is(err, ErrNotFound) {
			return apierr.Unauthorized()
		}
		if err != nil {
			return err
		}
		b, err := tx.BoardByID(seat.BoardID)
		if err != nil {
			return err
		}
		// The board comes first: a board the caller can't see says nothing about the
		// seat. One they still see, with the seat removed, says it was removed.
		if _, _, _, err := s.see(tx, p, b.Name); err != nil {
			if in.Code != "" && isBoardNotFound(err) {
				return joinCodeInvalid()
			}
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if seat.Status != StatusActive {
			return agentRemoved(seat, b.Name)
		}
		if in.Code != "" {
			normal, _ := ids.NormalizeJoinCode(in.Code)
			jc, err := tx.JoinCodeByDigest(ids.Digest(s.key, normal))
			if errors.Is(err, ErrNotFound) || (err == nil && jc.BoardID != b.ID) {
				return joinCodeInvalid()
			}
			if err != nil {
				return err
			}
			maker, err := codeMaker(tx, jc)
			if err != nil {
				return err
			}
			if h, err := tx.HumanByID(maker.HumanID); err != nil || h.RemovedAt != nil {
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				return joinCodeInvalid()
			}
			if jc.Kind == CodePairing && maker.HumanID != person.ID {
				return joinCodeInvalid()
			}
			owner, err := tx.HumanMember(b.ID, person.ID)
			if errors.Is(err, ErrNotFound) || (err == nil && owner.Status != StatusActive) {
				return joinCodeInvalid()
			}
			if err != nil {
				return err
			}
		} else if _, _, err := s.access(tx, p, b.Name); err != nil {
			return err
		}
		if seat.TokenDigest == nil || *seat.TokenDigest != ids.Digest(s.key, token) {
			return seatTokenReplaced(b.Name)
		}
		return nil
	})
}
