package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/boardfile"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// View is a board with the member who created it.
type View struct {
	Board   Board
	Creator Member
}

// NewBoard is a request to create a board.
type NewBoard struct {
	Name     string
	Template string
	Charter  string
	Preset   string
}

// CreateBoard creates a board, optionally from a template, and makes the calling human
// its first member and admin.
func (s *Service) CreateBoard(ctx context.Context, p Principal, in NewBoard) (View, error) {
	if err := requireHuman(p); err != nil {
		return View{}, err
	}
	var tf boardfile.File
	if in.Template != "" {
		var err error
		tf, err = boardfile.Template(in.Template)
		if errors.Is(err, boardfile.ErrNoTemplate) {
			return View{}, apierr.New(http.StatusUnprocessableEntity, "template_not_found", err.Error(),
				"Use one of the templates listed in this message.")
		}
		if err != nil {
			return View{}, err
		}
	}
	charter := tf.Charter
	if in.Charter != "" {
		charter = in.Charter
	}
	roles := map[string]rules.Role{}
	for name, r := range tf.Roles {
		roles[name] = r
	}
	if _, ok := roles[rules.MemberRole]; !ok {
		roles[rules.MemberRole] = rules.DefaultMemberRole()
	}
	change := tf.Policy
	if in.Preset != "" {
		change.Preset = in.Preset
	}
	if change.Preset == "" {
		change.Preset = rules.Starter
	}
	starter, _ := rules.Preset(rules.Starter)
	policy, err := starter.Apply(change)
	if err != nil {
		return View{}, invalid(err.Error(), "Use the starter or recommended preset.")
	}

	var view View
	err = s.st.Write(ctx, func(tx Tx) error {
		name := in.Name
		if name != "" {
			taken, err := tx.BoardNameTaken(name)
			if err != nil {
				return err
			}
			if taken {
				return apierr.New(http.StatusConflict, "board_name_taken", fmt.Sprintf("A board named %q already exists.", name),
					"Choose another name, or leave the name out to get a free one.")
			}
		} else {
			base := in.Template
			if base == "" {
				base = "board"
			}
			var lookupErr error
			name = rules.AllocateName(base, func(n string) bool {
				taken, err := tx.BoardNameTaken(n)
				if err != nil {
					lookupErr = err
				}
				return taken
			})
			if lookupErr != nil {
				return lookupErr
			}
		}
		now := s.clk.Now()
		boardID, err := s.gen.ID("brd", now)
		if err != nil {
			return err
		}
		memberID, err := s.gen.ID("mem", now)
		if err != nil {
			return err
		}
		var template *string
		if in.Template != "" {
			template = ptr(in.Template)
		}
		b := Board{
			ID: boardID, Name: name, Template: template, Charter: charter, Roles: roles, Policy: policy,
			HeadHash: events.GenesisHash, CreatedAt: stamp(now), CreatedBy: memberID,
		}
		if err := tx.InsertBoard(b); err != nil {
			return fmt.Errorf("insert board: %w", err)
		}
		creator := Member{
			ID: memberID, BoardID: boardID, Name: p.Human.Name, Kind: "human", HumanID: p.Human.ID,
			Access: rules.AccessAdmin, Status: "active", JoinedAt: stamp(now),
		}
		if _, err := s.append(tx, &b, events.BoardCreated, actorOf(creator), now, map[string]any{
			"board_id": b.ID, "name": b.Name, "template": b.Template, "charter": b.Charter, "roles": b.Roles, "policy": b.Policy,
		}); err != nil {
			return err
		}
		if err := s.addMember(tx, &b, creator, actorOf(creator), nil, now); err != nil {
			return err
		}
		view = View{Board: b, Creator: creator}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	s.notify.Changed(view.Board.ID)
	s.notify.Changed(boardsOfKey(p.Human.ID))
	return view, nil
}

// addMember stores a member and appends its member.joined event.
func (s *Service) addMember(tx Tx, b *Board, m Member, actor events.Actor, joinCodeID *string, at time.Time) error {
	if err := tx.InsertMember(m); err != nil {
		return fmt.Errorf("insert member: %w", err)
	}
	var access *string
	if m.Access != "" {
		access = ptr(m.Access)
	}
	_, err := s.append(tx, b, events.MemberJoined, actor, at, map[string]any{
		"member_id": m.ID, "name": m.Name, "kind": m.Kind, "role": m.Role, "owner": m.Owner,
		"harness": m.Harness, "access": access, "join_code_id": joinCodeID,
	})
	return err
}

// ListBoards returns the boards the caller is a member of.
func (s *Service) ListBoards(ctx context.Context, p Principal) ([]View, error) {
	var out []View
	err := s.st.Read(ctx, func(tx ReadTx) error {
		var boards []Board
		if p.Agent != nil {
			b, err := tx.BoardByID(p.Agent.BoardID)
			if err != nil {
				return err
			}
			boards = []Board{b}
		} else {
			var err error
			if boards, err = tx.BoardsOfHuman(p.Human.ID); err != nil {
				return err
			}
		}
		for _, b := range boards {
			v, err := viewOf(tx, b)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

func viewOf(tx ReadTx, b Board) (View, error) {
	members, err := tx.Members(b.ID)
	if err != nil {
		return View{}, err
	}
	for _, m := range members {
		if m.ID == b.CreatedBy {
			return View{Board: b, Creator: m}, nil
		}
	}
	return View{}, fmt.Errorf("board %s: creator %s missing", b.ID, b.CreatedBy)
}

// GetBoard returns one board the caller is a member of.
func (s *Service) GetBoard(ctx context.Context, p Principal, name string) (View, error) {
	var v View
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, _, err := access(tx, p, name)
		if err != nil {
			return err
		}
		v, err = viewOf(tx, b)
		return err
	})
	return v, err
}

// Members lists a board's members.
func (s *Service) Members(ctx context.Context, p Principal, name string) ([]Member, error) {
	var out []Member
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, _, err := access(tx, p, name)
		if err != nil {
			return err
		}
		if out, err = tx.Members(b.ID); err != nil {
			return err
		}
		if p.Agent != nil && !b.Policy.ShowHarness {
			for i := range out {
				out[i].Harness = nil
			}
		}
		return nil
	})
	return out, err
}

// UpdatePolicy changes a board's policy. Only the board's admins may.
func (s *Service) UpdatePolicy(ctx context.Context, p Principal, name string, change rules.PolicyChange) (View, error) {
	if err := requireHuman(p); err != nil {
		return View{}, err
	}
	var v View
	err := s.st.Write(ctx, func(tx Tx) error {
		b, me, err := access(tx, p, name)
		if err != nil {
			return err
		}
		if err := requireAdmin(tx, b, me, "change its policy"); err != nil {
			return err
		}
		after, err := b.Policy.Apply(change)
		if err != nil {
			return invalid(err.Error(), "Use aboard board policy starter or aboard board policy recommended.")
		}
		var applied *string
		if change.Preset != "" {
			applied = ptr(change.Preset)
		}
		now := s.clk.Now()
		if _, err := s.append(tx, &b, events.BoardPolicyChanged, actorOf(me), now, map[string]any{
			"before": b.Policy, "after": after, "preset_applied": applied,
		}); err != nil {
			return err
		}
		if err := tx.SetBoardPolicy(b.ID, after); err != nil {
			return err
		}
		b.Policy = after
		v, err = viewOf(tx, b)
		return err
	})
	if err != nil {
		return View{}, err
	}
	s.notify.Changed(v.Board.ID)
	return v, nil
}
