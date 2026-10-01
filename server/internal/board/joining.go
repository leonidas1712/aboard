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

// DefaultJoinCodeTTL is how long a join code works when no lifetime is given.
const DefaultJoinCodeTTL = 24 * time.Hour

// NewJoinCode is a created join code. Code and Line are only available at creation.
type NewJoinCode struct {
	JoinCode JoinCode
	Board    string
	Creator  Member
	Code     string
	Line     string
}

func roleNotFound(role string) *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, "role_not_found",
		fmt.Sprintf("This board has no role %q.", role), "Use a role defined in the board's charter and roles.")
}

func joinCodeInvalid() *apierr.Error {
	return apierr.New(http.StatusNotFound, "join_code_invalid",
		"That join code doesn't work: it is wrong, expired or revoked.", "Ask for a new join line from someone on the board.")
}

// CreateJoinCode makes a multi-use code that lets sessions join the board in role.
func (s *Service) CreateJoinCode(ctx context.Context, p Principal, boardName, role string, ttl time.Duration) (NewJoinCode, error) {
	if ttl == 0 {
		ttl = DefaultJoinCodeTTL
	}
	var out NewJoinCode
	err := s.st.Write(ctx, func(tx Tx) error {
		b, me, err := access(tx, p, boardName)
		if err != nil {
			return err
		}
		if p.Agent != nil && !b.Roles[*me.Role].Has(rules.Invite) {
			return apierr.New(http.StatusForbidden, "forbidden", "Your role can't invite new agents to this board.",
				"Ask a human on the board to create the join code.")
		}
		if _, ok := b.Roles[role]; !ok {
			return roleNotFound(role)
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
			ID: id, BoardID: b.ID, CodeDigest: ids.Digest(s.key, code), Role: role,
			ExpiresAt: stamp(now.Add(ttl)), CreatedAt: stamp(now), CreatedBy: me.ID,
		}
		if err := tx.InsertJoinCode(jc); err != nil {
			return fmt.Errorf("insert join code: %w", err)
		}
		if _, err := s.append(tx, &b, events.JoinCodeCreated, actorOf(me), now, map[string]any{
			"join_code_id": jc.ID, "role": jc.Role, "expires_at": jc.ExpiresAt,
		}); err != nil {
			return err
		}
		out = NewJoinCode{
			JoinCode: jc, Board: b.Name, Creator: me, Code: code,
			Line: joinline.Line{Board: b.Name, Server: s.cfg.JoinHost, Role: role, Code: code}.String(),
		}
		return nil
	})
	return out, err
}

// RevokeJoinCode stops a join code from working. Agents that already joined stay.
func (s *Service) RevokeJoinCode(ctx context.Context, p Principal, boardName, id string) (JoinCode, Member, error) {
	var jc JoinCode
	var creator Member
	err := s.st.Write(ctx, func(tx Tx) error {
		b, me, err := access(tx, p, boardName)
		if err != nil {
			return err
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
		members, err := tx.Members(b.ID)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.ID == jc.CreatedBy {
				creator = m
			}
		}
		return nil
	})
	return jc, creator, err
}

// JoinInput is a request for a new agent identity: either Code, or Board and Role from
// a human who is already a member.
type JoinInput struct {
	Code    string
	Board   string
	Role    string
	Name    string
	Harness string
}

// Joined is a new agent with its token, which is only available here.
type Joined struct {
	Agent Member
	Token string
	View  View
}

// Join creates a new agent owned by the calling human. If the human isn't a member of the
// board yet, they become one first, so every agent has a human on its board.
func (s *Service) Join(ctx context.Context, p Principal, in JoinInput) (Joined, error) {
	if err := requireHuman(p); err != nil {
		return Joined{}, err
	}
	var out Joined
	var ownerJoined bool
	err := s.st.Write(ctx, func(tx Tx) error {
		ownerJoined = false
		now := s.clk.Now()
		var b Board
		var role string
		var codeID *string
		switch {
		case in.Code != "":
			code, ok := ids.NormalizeJoinCode(in.Code)
			if !ok {
				return joinCodeInvalid()
			}
			jc, err := tx.JoinCodeByDigest(ids.Digest(s.key, code))
			if errors.Is(err, ErrNotFound) {
				return joinCodeInvalid()
			}
			if err != nil {
				return err
			}
			if jc.RevokedAt != nil || jc.ExpiresAt <= stamp(now) {
				return joinCodeInvalid()
			}
			if b, err = tx.BoardByID(jc.BoardID); err != nil {
				return err
			}
			role, codeID = jc.Role, ptr(jc.ID)
		case in.Board != "" && in.Role != "":
			var err error
			if b, _, err = access(tx, p, in.Board); err != nil {
				return err
			}
			if _, ok := b.Roles[in.Role]; !ok {
				return roleNotFound(in.Role)
			}
			role = in.Role
		default:
			return invalid("A join needs a code, or a board and a role.", "Paste the whole join line into aboard join.")
		}

		taken := func(n string) bool { _, err := tx.MemberByName(b.ID, n); return err == nil }
		owner, err := tx.HumanMember(b.ID, p.Human.ID)
		if errors.Is(err, ErrNotFound) {
			owner = Member{
				BoardID: b.ID, Name: rules.AllocateName(p.Human.Name, taken), Kind: "human", HumanID: p.Human.ID,
				Status: "active", JoinedAt: stamp(now),
			}
			if owner.ID, err = s.gen.ID("mem", now); err != nil {
				return err
			}
			if err := s.addMember(tx, &b, owner, actorOf(owner), nil, now); err != nil {
				return err
			}
			ownerJoined = true
		} else if err != nil {
			return err
		}

		name := in.Name
		if name != "" {
			if taken(name) {
				return apierr.New(http.StatusConflict, "name_taken", fmt.Sprintf("Someone on this board is already called %q.", name),
					"Choose another name, or leave the name out to get a free one.")
			}
		} else {
			name = rules.AllocateName(role, taken)
		}
		token, err := s.gen.Token("aba")
		if err != nil {
			return err
		}
		agent := Member{
			BoardID: b.ID, Name: name, Kind: "agent", Role: ptr(role), HumanID: p.Human.ID, Owner: ptr(p.Human.Name),
			TokenDigest: ptr(ids.Digest(s.key, token)), Status: "active", JoinedAt: stamp(now),
			// A new agent starts reading at the board's head: earlier messages are in the
			// timeline, not its inbox.
			Cursor: b.HeadSeq + 1,
		}
		if in.Harness != "" {
			agent.Harness = ptr(in.Harness)
		}
		if agent.ID, err = s.gen.ID("mem", now); err != nil {
			return err
		}
		if err := s.addMember(tx, &b, agent, actorOf(owner), codeID, now); err != nil {
			return err
		}
		view, err := viewOf(tx, b)
		if err != nil {
			return err
		}
		out = Joined{Agent: agent, Token: token, View: view}
		return nil
	})
	if err != nil {
		return Joined{}, err
	}
	s.notify.Changed(out.View.Board.ID)
	if ownerJoined {
		s.notify.Changed(boardsOfKey(p.Human.ID))
	}
	return out, nil
}
