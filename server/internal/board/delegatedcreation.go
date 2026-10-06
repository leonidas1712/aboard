package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// CreationReceipt commits the replay result with its board and seat, so a failed
// response-cache write cannot turn a lost answer into another board.
type CreationReceipt struct {
	DelegationID string
	Key          string
	RequestHash  string
	CreatedAt    string
	Joined       Joined
}

// DelegatedBoard is the board setup and session the machine vouches for.
type DelegatedBoard struct {
	NewBoard
	Session, Harness, Role, AgentName string
	Key, RequestHash                  string
}

// CreateDelegatedBoard creates a person-owned board and its session seat atomically.
// The replay flag reports a receipt recovery; it never rotates the seat's token.
func (s *Service) CreateDelegatedBoard(ctx context.Context, p Principal, in DelegatedBoard) (Joined, bool, error) {
	if p.Delegation == nil {
		return Joined{}, false, delegationForbidden()
	}
	if in.Key == "" || len(in.Key) > 128 || in.RequestHash == "" {
		return Joined{}, false, invalid("A delegated creation needs an Idempotency-Key.", "Retry the same creation with the same key and body.")
	}
	if in.Session == "" || in.Harness == "" || !strings.HasPrefix(in.Session, in.Harness+":") {
		return Joined{}, false, invalid("The harness must match the session's prefix.", "Send a session as <harness>:<id> and that harness.")
	}
	p = p.asDelegate()
	prepared, err := prepareBoard(in.NewBoard)
	if err != nil {
		return Joined{}, false, err
	}
	role := in.Role
	if role == "" {
		role = rules.MemberRole
	}
	var joined Joined
	replayed := false
	var person Human
	err = s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		var err error
		person, err = caller(tx, p, stamp(now))
		if err != nil {
			return err
		}
		receipt, err := tx.DelegatedCreation(p.Delegation.ID, in.Key)
		if err == nil {
			at, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt)
			if err != nil {
				return fmt.Errorf("read creation receipt time: %w", err)
			}
			if now.Before(at.Add(24 * time.Hour)) {
				if receipt.RequestHash != in.RequestHash {
					return apierr.New(http.StatusUnprocessableEntity, "idempotency_conflict", "This Idempotency-Key was already used for a different request.", "Use a new Idempotency-Key for a new request.")
				}
				if err := s.checkCreationReplay(tx, p, receipt.Joined); err != nil {
					return err
				}
				joined, replayed = receipt.Joined, true
				return nil
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := mayCreateBoards(tx, person); err != nil {
			return err
		}
		if _, ok := prepared.Roles[role]; !ok {
			return roleNotFound(role)
		}
		seatID, err := s.gen.ID("mem", now)
		if err != nil {
			return err
		}
		via := map[string]any{"via": "delegation", "delegation_id": p.Delegation.ID, "agent_id": seatID}
		view, err := s.createBoard(tx, person, in.NewBoard, prepared, now, via)
		if err != nil {
			return err
		}
		join := JoinInput{Name: in.AgentName, Harness: in.Harness, Session: in.Session, seatID: seatID}
		joined, err = s.seat(tx, &view.Board, view.Creator, person.Name, join, role, ptr(p.Delegation.KeyID), nil, now, map[string]any{"via": "delegation", "delegation_id": p.Delegation.ID})
		if err != nil {
			return err
		}
		joined.View.CanArchive = true
		return tx.SaveDelegatedCreation(CreationReceipt{DelegationID: p.Delegation.ID, Key: in.Key, RequestHash: in.RequestHash, CreatedAt: stamp(now), Joined: joined})
	})
	if err != nil {
		return Joined{}, false, err
	}
	if !replayed {
		s.notify.Changed(joined.View.Board.ID)
		s.notify.Changed(boardsOfKey(person.ID))
	}
	return joined, replayed, nil
}

func (s *Service) checkCreationReplay(tx ReadTx, p Principal, joined Joined) error {
	person, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return err
	}
	b, err := tx.BoardByID(joined.View.Board.ID)
	if errors.Is(err, ErrNotFound) {
		return apierr.BoardNotFound("")
	}
	if err != nil {
		return err
	}
	creator, err := tx.HumanMember(b.ID, person.ID)
	if errors.Is(err, ErrNotFound) || (err == nil && (creator.Status != StatusActive || creator.ID != b.CreatedBy || lifecycleOf(b) == LifecycleDeleted)) {
		return apierr.BoardNotFound("")
	}
	if err != nil {
		return err
	}
	if err := requireActive(b); err != nil {
		return err
	}
	seat, err := tx.MemberByID(joined.Agent.ID)
	if errors.Is(err, ErrNotFound) {
		return apierr.BoardNotFound("")
	}
	if err != nil {
		return err
	}
	if seat.BoardID != b.ID || seat.HumanID != person.ID {
		return apierr.BoardNotFound("")
	}
	if seat.Status != StatusActive {
		return agentRemoved(seat, b.Name)
	}
	if seat.KeyID == nil || *seat.KeyID != p.Delegation.KeyID || seat.TokenDigest == nil || *seat.TokenDigest != ids.Digest(s.key, joined.Token) {
		return seatTokenReplaced(b.Name)
	}
	return nil
}

// CheckCreationReplay rechecks the immutable token-bearing answer with current
// delegation, creator access, lifecycle and seat authority before it leaves storage.
func (s *Service) CheckCreationReplay(ctx context.Context, p Principal, boardID, memberID, token string) error {
	if p.Delegation == nil {
		return delegationForbidden()
	}
	return s.st.Read(ctx, func(tx ReadTx) error {
		return s.checkCreationReplay(tx, p.asDelegate(), Joined{Agent: Member{ID: memberID}, View: View{Board: Board{ID: boardID}}, Token: token})
	})
}

// CheckDelegation checks current delegation authority for a cached creation refusal.
func (s *Service) CheckDelegation(ctx context.Context, p Principal) error {
	if p.Delegation == nil {
		return delegationForbidden()
	}
	return s.st.Read(ctx, func(tx ReadTx) error { return stillValid(tx, p.asDelegate(), stamp(s.clk.Now())) })
}
