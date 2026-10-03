package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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

// ShowsCounts reports whether p may see how many messages the board holds: people read
// every message, and so do agents while visibility is open. Under addressed visibility
// a count would tell an agent how many messages it can't read.
func (v View) ShowsCounts(p Principal) bool {
	return p.Agent == nil || v.Board.Policy.Visibility == rules.VisibilityOpen
}

// NewBoard is a request to create a board.
type NewBoard struct {
	Name     string
	Title    string
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
	title, err := cleanTitle(in.Title)
	if err != nil {
		return View{}, err
	}
	var tf boardfile.File
	if in.Template != "" {
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
			ID: boardID, Name: name, Title: title, Template: template, Charter: charter, Roles: roles, Policy: policy,
			HeadHash: events.GenesisHash, CreatedAt: stamp(now), CreatedBy: memberID,
		}
		if err := tx.InsertBoard(b); err != nil {
			return fmt.Errorf("insert board: %w", err)
		}
		creator := Member{
			ID: memberID, BoardID: boardID, Name: p.Human.Name, Kind: "human", HumanID: p.Human.ID,
			Access: rules.AccessAdmin, Status: "active", JoinedAt: stamp(now),
		}
		created := map[string]any{
			"board_id": b.ID, "name": b.Name, "template": b.Template, "charter": b.Charter, "roles": b.Roles, "policy": b.Policy,
		}
		if title != nil {
			created["title"] = *title
		}
		if _, err := s.append(tx, &b, events.BoardCreated, actorOf(creator), now, created); err != nil {
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

// Members lists a board's members, each agent with its current presence.
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
		now := s.clk.Now()
		for i := range out {
			out[i].Presence = out[i].CurrentPresence(now)
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

// maxTitle is the longest title a board may have, in characters.
const maxTitle = 80

// cleanTitle trims a title and checks it is one line of at most maxTitle characters.
// An empty title is no title: nil.
func cleanTitle(title string) (*string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(title) > maxTitle {
		return nil, invalid(fmt.Sprintf("A board's title can be at most %d characters.", maxTitle), "Shorten the title.")
	}
	if strings.IndexFunc(title, unicode.IsControl) >= 0 {
		return nil, invalid("A board's title must be one line of text.", "Remove the line breaks and tabs from the title.")
	}
	return &title, nil
}

// Change is what an admin changes on a board. Nil fields stay as they are.
type Change struct {
	// Title is the new title; an empty string removes it.
	Title  *string
	Policy *rules.PolicyChange
}

// UpdateBoard changes a board's title, policy or both. Only the board's admins may.
// Each change appends its own event; a title that doesn't change appends nothing.
func (s *Service) UpdateBoard(ctx context.Context, p Principal, name string, change Change) (View, error) {
	if err := requireHuman(p); err != nil {
		return View{}, err
	}
	var title *string
	if change.Title != nil {
		var err error
		if title, err = cleanTitle(*change.Title); err != nil {
			return View{}, err
		}
	}
	var v View
	err := s.st.Write(ctx, func(tx Tx) error {
		b, me, err := access(tx, p, name)
		if err != nil {
			return err
		}
		what := "change its policy"
		if change.Policy == nil {
			what = "change its title"
		}
		if err := requireAdmin(tx, b, me, what); err != nil {
			return err
		}
		now := s.clk.Now()
		if change.Title != nil && deref(title) != deref(b.Title) {
			if _, err := s.append(tx, &b, events.BoardTitled, actorOf(me), now, map[string]any{
				"before": b.Title, "after": title,
			}); err != nil {
				return err
			}
			if err := tx.SetBoardTitle(b.ID, title); err != nil {
				return err
			}
			b.Title = title
		}
		if change.Policy != nil {
			after, err := b.Policy.Apply(*change.Policy)
			if err != nil {
				return invalid(err.Error(), "Use aboard board policy starter or aboard board policy recommended.")
			}
			var applied *string
			if change.Policy.Preset != "" {
				applied = ptr(change.Policy.Preset)
			}
			if _, err := s.append(tx, &b, events.BoardPolicyChanged, actorOf(me), now, map[string]any{
				"before": b.Policy, "after": after, "preset_applied": applied,
			}); err != nil {
				return err
			}
			if err := tx.SetBoardPolicy(b.ID, after); err != nil {
				return err
			}
			b.Policy = after
		}
		v, err = viewOf(tx, b)
		return err
	})
	if err != nil {
		return View{}, err
	}
	s.notify.Changed(v.Board.ID)
	return v, nil
}

// Me is who a token acts as. Board is set for an agent.
type Me struct {
	Human   *Human
	Agent   *Member
	Board   string
	Browser bool
}

// WhoAmI returns who the caller is: a person (through their login or a browser), or an
// agent with its board.
func (s *Service) WhoAmI(ctx context.Context, p Principal) (Me, error) {
	me := Me{Human: p.Human, Agent: p.Agent, Browser: p.Browser}
	if p.Agent == nil {
		return me, nil
	}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, err := tx.BoardByID(p.Agent.BoardID)
		me.Board = b.Name
		return err
	})
	return me, err
}
