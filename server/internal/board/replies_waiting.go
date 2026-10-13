package board

import (
	"context"
	"sort"
)

// RepliesWaitingFilter narrows the person's requests to one board and a list limit.
type RepliesWaitingFilter struct {
	Board string
	Limit int
}

// ReplyWaiting pairs a pending request with its current board and reader.
type ReplyWaiting struct {
	Board   Board
	Reader  Member
	Message Message
}

// RepliesWaitingListing includes the unpaged request count for an Inbox badge.
type RepliesWaitingListing struct {
	Requests []ReplyWaiting
	Count    int64
	More     bool
}

// RepliesWaiting reads requests through the person's current board memberships.
func (s *Service) RepliesWaiting(ctx context.Context, p Principal, f RepliesWaitingFilter) (RepliesWaitingListing, error) {
	out := RepliesWaitingListing{Requests: []ReplyWaiting{}}
	if err := humanOnly(p, "read your replies waiting", "aboard read"); err != nil {
		return out, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		person, err := caller(tx, p, stamp(s.clk.Now()))
		if err != nil {
			return err
		}
		var boards []Board
		if f.Board != "" {
			b, _, err := s.access(tx, p, f.Board)
			if err != nil {
				return err
			}
			boards = []Board{b}
		} else {
			boards, err = tx.BoardsOfHuman(person.ID)
			if err != nil {
				return err
			}
		}
		for _, b := range boards {
			if lifecycleOf(b) != LifecycleActive {
				continue
			}
			_, me, err := s.access(tx, p, b.Name)
			if err != nil {
				return err
			}
			msgs, count, err := tx.RepliesWaiting(me, limit)
			if err != nil {
				return err
			}
			if err := s.annotate(tx, b, me, msgs); err != nil {
				return err
			}
			out.Count += count
			for _, m := range msgs {
				out.Requests = append(out.Requests, ReplyWaiting{b, me, m})
			}
		}
		sort.Slice(out.Requests, func(i, j int) bool {
			a, b := out.Requests[i].Message, out.Requests[j].Message
			if a.At == b.At {
				return a.ID > b.ID
			}
			return a.At > b.At
		})
		if len(out.Requests) > limit {
			out.Requests = out.Requests[:limit]
		}
		out.More = out.Count > int64(len(out.Requests))
		return nil
	})
	return out, err
}
