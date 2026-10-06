package board

import (
	"context"
	"errors"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// BoardReplay identifies the data held by an immutable cached response. The adapter
// supplies identifiers, never authority; the read transaction checks the caller again.
type BoardReplay struct {
	Name       string
	ID         string
	MessageID  string
	JoinCode   string
	OwnSeat    bool
	Lifecycle  string
	DeleteDone bool
}

// CheckBoardReplay checks current read access before a cached board response is sent.
// A successful deletion receipt is the only response allowed through a tombstone.
func (s *Service) CheckBoardReplay(ctx context.Context, p Principal, in BoardReplay) error {
	return s.st.Read(ctx, func(tx ReadTx) error {
		if _, err := caller(tx, p, stamp(s.clk.Now())); err != nil {
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
