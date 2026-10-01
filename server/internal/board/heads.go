package board

import (
	"context"
	"fmt"
	"reflect"
	"time"
)

// Head is where a board's event log ends: the seq of its newest event.
type Head struct {
	BoardID string
	Board   string // the board's name
	Seq     int64
}

// boardsOfKey is the Notifier key that changes when the human becomes a member of a
// board. Board ids start with "brd_", so it never names a board.
func boardsOfKey(humanID string) string { return "boards-of/" + humanID }

// Heads returns the head of every board the human is a human member of, by board name.
// Only humans may follow heads: an agent reads its own board.
func (s *Service) Heads(ctx context.Context, p Principal) ([]Head, error) {
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	var out []Head
	err := s.st.Read(ctx, func(tx ReadTx) error {
		boards, err := tx.BoardsOfHuman(p.Human.ID)
		if err != nil {
			return err
		}
		out = make([]Head, 0, len(boards))
		for _, b := range boards {
			out = append(out, Head{BoardID: b.ID, Board: b.Name, Seq: b.HeadSeq})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read heads: %w", err)
	}
	return out, nil
}

// HeadFeed follows the heads of one human's boards, including boards the human joins
// while following. It holds no goroutines; each call to Next does its own waiting.
type HeadFeed struct {
	s    *Service
	p    Principal
	sent map[string]int64 // board id → the last head seq returned
}

// FollowHeads starts following the heads of the human's boards. Only humans may.
func (s *Service) FollowHeads(p Principal) (*HeadFeed, error) {
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	return &HeadFeed{s: s, p: p, sent: map[string]int64{}}, nil
}

// Next returns the heads that moved since the last call; the first call returns every
// board's head. When none moved, it waits until one does, the human joins a board, or
// tick fires. On tick it reads the heads once more, so a board joined without a signal
// is still found, and returns what moved (possibly nothing) with ticked true.
func (f *HeadFeed) Next(ctx context.Context, tick <-chan time.Time) (moved []Head, ticked bool, err error) {
	for {
		// Watch before reading, so a change between the read and the wait isn't missed.
		cases := []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(tick)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(boardsOfKey(f.p.Human.ID)))},
		}
		for id := range f.sent {
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(id))})
		}
		if moved, err = f.read(ctx); err != nil || len(moved) > 0 {
			return moved, false, err
		}
		switch chosen, _, _ := reflect.Select(cases); chosen {
		case 0:
			return nil, false, fmt.Errorf("follow heads: %w", ctx.Err())
		case 1:
			moved, err = f.read(ctx)
			return moved, true, err
		}
	}
}

// read returns the heads that differ from those last returned and remembers them.
// Boards the human is no longer on are forgotten.
func (f *HeadFeed) read(ctx context.Context) ([]Head, error) {
	heads, err := f.s.Heads(ctx, f.p)
	if err != nil {
		return nil, err
	}
	var moved []Head
	current := make(map[string]int64, len(heads))
	for _, h := range heads {
		current[h.BoardID] = h.Seq
		if seq, ok := f.sent[h.BoardID]; !ok || seq != h.Seq {
			moved = append(moved, h)
		}
	}
	f.sent = current
	return moved, nil
}
