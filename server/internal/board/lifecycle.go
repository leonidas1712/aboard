package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
)

// Board lifecycles preserve the record while controlling new work and access.
const (
	LifecycleActive   = "active"
	LifecycleArchived = "archived"
	LifecycleDeleted  = "deleted"
)

// LifecycleResult is a receipt of one committed operation, never a content response.
type LifecycleResult struct {
	ID        string
	Lifecycle string
	Changed   bool
}

func lifecycleOf(b Board) string {
	if b.Lifecycle == "" {
		return LifecycleActive
	}
	return b.Lifecycle
}

func requireActive(b Board) error {
	if lifecycleOf(b) == LifecycleDeleted {
		return apierr.BoardNotFound(b.Name)
	}
	if lifecycleOf(b) == LifecycleArchived {
		return apierr.New(http.StatusConflict, "board_archived", "This board is archived and refuses new content and access grants.", "Ask its creator while still on it, or a server admin, to restore it: aboard board restore --board "+b.Name+".")
	}
	return nil
}

func creatorRequired() error {
	return apierr.New(http.StatusForbidden, "board_creator_required", "Only the board's creator while still on it, or a server admin, may do that.", "Ask its creator or a server admin to archive, restore or delete it.")
}

func (s *Service) lifecycleTarget(tx ReadTx, p Principal, selector string, allowDeleted bool) (Board, Member, Human, error) {
	if p.Delegation != nil {
		return Board{}, Member{}, Human{}, delegationForbidden()
	}
	person, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return Board{}, Member{}, Human{}, err
	}
	var b Board
	if strings.HasPrefix(selector, "brd_") {
		b, err = tx.BoardByID(selector)
	} else {
		b, err = tx.BoardByName(selector)
	}
	if errors.Is(err, ErrNotFound) {
		return Board{}, Member{}, Human{}, apierr.BoardNotFound(selector)
	}
	if err != nil {
		return Board{}, Member{}, Human{}, err
	}
	if lifecycleOf(b) == LifecycleDeleted && !allowDeleted {
		return Board{}, Member{}, Human{}, apierr.BoardNotFound(selector)
	}
	me, err := tx.HumanMember(b.ID, person.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Board{}, Member{}, Human{}, err
	}
	on := err == nil && me.Status == StatusActive
	if p.Agent != nil {
		if p.Agent.BoardID != b.ID || !on || lifecycleOf(b) == LifecycleDeleted {
			return Board{}, Member{}, Human{}, apierr.BoardNotFound(selector)
		}
		if _, _, err = seatOf(tx, *p.Agent); err != nil {
			return Board{}, Member{}, Human{}, err
		}
	} else if !on && b.Visibility == BoardPrivate && (person.Role != ServerAdmin || selector != b.ID) {
		return Board{}, Member{}, Human{}, apierr.BoardNotFound(selector)
	}
	if person.Role == ServerGuest {
		if !on {
			return Board{}, Member{}, Human{}, apierr.BoardNotFound(selector)
		}
		return Board{}, Member{}, Human{}, guestNotAllowed("manage a board's lifecycle")
	}
	if (!on || me.ID != b.CreatedBy) && (p.Agent != nil || person.Role != ServerAdmin) {
		return Board{}, Member{}, Human{}, creatorRequired()
	}
	return b, me, person, nil
}

func (s *Service) capabilities(tx ReadTx, p Principal, b Board) (archive, restore, deleteBoard bool, err error) {
	if p.Delegation != nil || lifecycleOf(b) == LifecycleDeleted {
		return false, false, false, nil
	}
	if _, _, _, err := s.lifecycleTarget(tx, p, b.ID, false); err != nil {
		if e, ok := apierr.As(err); ok && (e.Status == http.StatusForbidden || e.Status == http.StatusNotFound) {
			return false, false, false, nil
		}
		return false, false, false, err
	}
	return lifecycleOf(b) == LifecycleActive, lifecycleOf(b) == LifecycleArchived, p.Agent == nil && lifecycleOf(b) == LifecycleArchived, nil
}

// ArchiveBoard freezes new work without revoking access to the existing record.
func (s *Service) ArchiveBoard(ctx context.Context, p Principal, selector string) (LifecycleResult, error) {
	return s.changeLifecycle(ctx, p, selector, LifecycleArchived)
}

// RestoreBoard makes an archived board active without reviving removed identities.
func (s *Service) RestoreBoard(ctx context.Context, p Principal, selector string) (LifecycleResult, error) {
	return s.changeLifecycle(ctx, p, selector, LifecycleActive)
}

// DeleteBoard leaves a tombstone after an archived board and ends access to its record.
func (s *Service) DeleteBoard(ctx context.Context, p Principal, selector string) (LifecycleResult, error) {
	if p.Delegation != nil {
		return LifecycleResult{}, delegationForbidden()
	}
	if err := humanOnly(p, "delete a board", "aboard board delete --board "+selector); err != nil {
		return LifecycleResult{}, err
	}
	return s.changeLifecycle(ctx, p, selector, LifecycleDeleted)
}

func (s *Service) changeLifecycle(ctx context.Context, p Principal, selector, target string) (LifecycleResult, error) {
	var out LifecycleResult
	var people []string
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, person, err := s.lifecycleTarget(tx, p, selector, false)
		if err != nil {
			return err
		}
		before := lifecycleOf(b)
		if target == LifecycleDeleted && before != LifecycleArchived {
			return apierr.New(http.StatusConflict, "board_not_archived", "Archive the board before deleting it.", "Run aboard board archive --board "+selector+" first.")
		}
		out = LifecycleResult{ID: b.ID, Lifecycle: target, Changed: before != target}
		if !out.Changed {
			return nil
		}
		actor := actorOf(me)
		if p.Agent != nil {
			actor = actorOf(*p.Agent)
		} else if me.ID == "" || me.Status != StatusActive {
			actor = events.Actor{Kind: "human", Name: ptr(person.Name)}
		}
		typ := events.BoardArchived
		if target == LifecycleActive {
			typ = events.BoardRestored
		}
		if target == LifecycleDeleted {
			typ = events.BoardDeleted
		}
		if _, err = s.append(tx, &b, typ, actor, s.clk.Now(), map[string]any{"person_id": person.ID, "before": before, "after": target}); err != nil {
			return err
		}
		if err := tx.SetBoardLifecycle(b.ID, target); err != nil {
			return err
		}
		members, err := tx.Members(b.ID)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.Kind == "human" && m.Status == StatusActive {
				people = append(people, m.HumanID)
			}
			if target == LifecycleDeleted && m.Kind == "agent" && m.Status == StatusActive {
				if err := tx.SetMemberStatus(m.ID, StatusRemoved); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("change board lifecycle: %w", err)
	}
	if out.Changed {
		s.notify.Changed(out.ID)
		for _, id := range people {
			s.notify.Changed(boardsOfKey(id))
		}
	}
	return out, nil
}

func (s *Service) filterListing(tx ReadTx, p Principal, out *Listing, filter string) error {
	visible := out.Boards[:0]
	for _, v := range out.Boards {
		state := lifecycleOf(v.Board)
		if state == LifecycleDeleted {
			continue
		}
		if state == LifecycleArchived {
			out.ArchivedCount++
		}
		if filter != "all" && state != filter {
			continue
		}
		v.Board.Lifecycle = state
		var err error
		v.CanArchive, v.CanRestore, v.CanDelete, err = s.capabilities(tx, p, v.Board)
		if err != nil {
			return err
		}
		visible = append(visible, v)
	}
	out.Boards = visible
	hidden := out.Hidden[:0]
	for _, v := range out.Hidden {
		b, err := tx.BoardByID(v.ID)
		if err != nil {
			return err
		}
		state := lifecycleOf(b)
		if state == LifecycleDeleted {
			continue
		}
		if filter != "all" && state != filter {
			continue
		}
		v.Lifecycle = state
		v.CanArchive, v.CanRestore, v.CanDelete, err = s.capabilities(tx, p, b)
		if err != nil {
			return err
		}
		hidden = append(hidden, v)
	}
	out.Hidden = hidden
	return nil
}
