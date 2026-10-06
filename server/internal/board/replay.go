package board

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// Replay identifies the data held by an immutable cached response. The adapter
// supplies identifiers, never authority; the read transaction checks the caller again.
type Replay struct {
	AddPeople  bool
	Handle     string
	Name       string
	ID         string
	MessageID  string
	JoinCode   string
	OwnSeat    bool
	Lifecycle  string
	DeleteDone bool
	// AgentRemoval is an agent removed from the board Name names (by name or id).
	AgentRemoval bool
	// PruneAll is a prune across the server, which only a server admin may see again.
	PruneAll bool
}

// HiddenBoards returns which of the boards, by id, the caller can't see now: a private
// board they aren't on, a deleted or missing one, or for a guest any board they aren't
// on. A cached answer naming such a board has its names withheld before it is sent
// again, since the caller may have seen the board when it was first made.
func (s *Service) HiddenBoards(ctx context.Context, p Principal, boardIDs []string) (map[string]bool, error) {
	hidden := map[string]bool{}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		for _, id := range boardIDs {
			b, err := tx.BoardByID(id)
			if errors.Is(err, ErrNotFound) {
				hidden[id] = true
				continue
			}
			if err != nil {
				return err
			}
			me, err := tx.HumanMember(b.ID, person.ID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			on := err == nil && me.Status == StatusActive
			open := b.Visibility == BoardOpen && person.Role != ServerGuest
			hidden[id] = lifecycleOf(b) == LifecycleDeleted || (!on && !open)
		}
		return nil
	})
	return hidden, err
}

// CheckBoardReplay checks current read access before a cached board response is sent.
// A successful deletion receipt is the only response allowed through a tombstone.
func (s *Service) CheckBoardReplay(ctx context.Context, p Principal, in Replay) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if in.PruneAll && person.Role != ServerAdmin {
			return apierr.New(http.StatusForbidden, "server_admin_required",
				"Only an admin of this server can prune agents across the server.", "Prune your own agents: aboard agent prune.")
		}
		if in.AgentRemoval {
			_, _, _, err := s.removalBoard(tx, p, person, in.Name)
			return err
		}
		if in.AddPeople {
			_, _, _, err := s.addPersonAuthority(tx, p, in.Name, in.Handle)
			return err
		}
		if in.Lifecycle != "" {
			if in.Lifecycle == "delete" {
				if err := requireHuman(p); err != nil {
					return err
				}
			}
			_, _, _, err := s.lifecycleTarget(tx, p, in.Name, in.Lifecycle == "delete" && in.DeleteDone)
			return err
		}
		if in.OwnSeat {
			if p.Agent == nil {
				return nil
			}
			_, _, err := seatOf(tx, *p.Agent)
			return err
		}
		name := in.Name
		if in.JoinCode != "" {
			normal, _ := ids.NormalizeJoinCode(in.JoinCode)
			code, err := tx.JoinCodeByDigest(ids.Digest(s.key, normal))
			if errors.Is(err, ErrNotFound) {
				return joinCodeInvalid()
			}
			if err != nil {
				return err
			}
			if _, err := codeMaker(tx, code); err != nil {
				return err
			}
			in.ID = code.BoardID
		}
		if in.ID != "" {
			b, err := tx.BoardByID(in.ID)
			if errors.Is(err, ErrNotFound) {
				return apierr.BoardNotFound(name)
			}
			if err != nil {
				return err
			}
			name = b.Name
		}
		if name != "" {
			if _, _, _, err := s.see(tx, p, name); err != nil {
				if isBoardNotFound(err) {
					if in.JoinCode != "" {
						return joinCodeInvalid()
					}
					// A generated name loaded from a saved creation result is not a
					// name the now-excluded caller supplied or may discover.
					return apierr.BoardNotFound(in.Name)
				}
				return err
			}
		}
		if strings.HasPrefix(in.MessageID, "msg_") {
			_, _, _, err := s.visibleMessage(tx, p, in.MessageID)
			return err
		}
		return nil
	})
}
