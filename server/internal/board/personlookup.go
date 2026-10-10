package board

import (
	"context"
	"errors"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// LookupPerson resolves a current handle without giving agents the server directory.
func (s *Service) LookupPerson(ctx context.Context, p Principal, handle string) (Human, error) {
	var out Human
	err := s.st.Read(ctx, func(tx ReadTx) error {
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		if person.Role == ServerGuest {
			return guestNotAllowed("look up the server's people")
		}
		if p.Agent != nil {
			if _, _, err := seatOf(tx, *p.Agent); err != nil {
				return err
			}
		}
		out, err = tx.HumanByName(handle)
		if errors.Is(err, ErrNotFound) || (err == nil && (out.RemovedAt != nil || out.Role == ServerGuest)) {
			return apierr.New(404, "person_not_found", "No person with that handle is available.", "Check the person's current handle with your team.")
		}
		return err
	})
	return out, err
}
