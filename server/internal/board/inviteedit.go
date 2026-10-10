package board

import (
	"context"
	"errors"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

func (s *Service) editableInvite(tx ReadTx, p Principal, id string) (ServerInvite, error) {
	if err := renameCredential(p); err != nil {
		return ServerInvite{}, err
	}
	h, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return ServerInvite{}, err
	}
	if p.Agent != nil {
		b, _, err := seatOf(tx, *p.Agent)
		if err != nil {
			return ServerInvite{}, err
		}
		if err := requireActive(b); err != nil {
			return ServerInvite{}, err
		}
	}
	inv, err := tx.ServerInviteByID(id)
	if errors.Is(err, ErrNotFound) || (err == nil && inv.CreatedBy != h.ID) {
		return ServerInvite{}, apierr.New(http.StatusNotFound, "invite_not_found", "That invitation wasn't issued by you.", "Use the id of your own outstanding invitation.")
	}
	if err != nil {
		return ServerInvite{}, err
	}
	if _, _, err := s.validServerInviteRecord(tx, inv); err != nil {
		var refusal *apierr.Error
		if !errors.As(err, &refusal) {
			return ServerInvite{}, err
		}
		return ServerInvite{}, apierr.New(http.StatusConflict, "invite_unavailable", "That invitation can no longer be edited.", "Use a current outstanding invitation; redeemed accounts keep their own identity.")
	}
	return inv, nil
}

// EditServerInvite changes display metadata without changing the issued capability.
func (s *Service) EditServerInvite(ctx context.Context, p Principal, id, handle string) (ServerInvite, error) {
	if handle != "" && !validName(handle) {
		return ServerInvite{}, invalid("The suggested handle is invalid.", "Use lowercase letters, digits and single dashes, at most 40 characters.")
	}
	var out ServerInvite
	err := s.writeAs(ctx, p, func(tx Tx) error {
		inv, err := s.editableInvite(tx, p, id)
		if err != nil {
			return err
		}
		if err := tx.SetServerInviteHandle(id, handle); err != nil {
			return err
		}
		inv.SuggestedHandle = handle
		inv.Boards, err = s.visibleInviteBoards(tx, p, inv.Boards)
		if err != nil {
			return err
		}
		out = inv
		return nil
	})
	return out, err
}

// CheckInviteEditReplay refuses cached metadata that the caller can no longer see.
func (s *Service) CheckInviteEditReplay(ctx context.Context, p Principal, id string, boards []string) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		if _, err := s.editableInvite(tx, p, id); err != nil {
			return err
		}
		visible, err := s.visibleInviteBoards(tx, p, boards)
		if err != nil {
			return err
		}
		if len(visible) != len(boards) {
			return apierr.BoardNotFound("")
		}
		return nil
	})
}

func (s *Service) visibleInviteBoards(tx ReadTx, p Principal, ids []string) ([]string, error) {
	out := []string{}
	for _, id := range ids {
		b, err := tx.BoardByID(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, _, _, err := s.see(tx, p, b.Name); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}
