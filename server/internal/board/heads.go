package board

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
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
		// A stream reads with its credential checked in the same transaction.
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
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
// while following, and the presence of the agents on them. It holds no goroutines;
// each call to Next does its own waiting.
type HeadFeed struct {
	s    *Service
	p    Principal
	sent map[string]int64 // board id → the last head seq returned
	// presence is, by board id and agent name, the presence last returned, or seen
	// when the board was first read.
	presence map[string]map[string]Presence
	// reads is, by board id and agent name, the read position of the human's own agents
	// last returned, or seen when the board was first read.
	reads map[string]map[string]int64
}

// FollowHeads starts following the heads of the human's boards. Only humans may.
func (s *Service) FollowHeads(p Principal) (*HeadFeed, error) {
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	return &HeadFeed{s: s, p: p, sent: map[string]int64{}, presence: map[string]map[string]Presence{}, reads: map[string]map[string]int64{}}, nil
}

// Update is what changed on a human's boards: heads that moved, agents whose presence
// changed, and the human's own agents whose read position moved.
type Update struct {
	Heads    []Head
	Presence []PresenceChange
	Reads    []ReadChange
}

func (u Update) empty() bool { return len(u.Heads) == 0 && len(u.Presence) == 0 && len(u.Reads) == 0 }

// ReadChange is the read position of one of the human's agents that moved since a
// HeadFeed last looked: by its own inbox acknowledgement, its owner's delivery daemon's,
// or any other client's. Only the agent's owner is told.
type ReadChange struct {
	BoardID string
	Board   string // the board's name
	Agent   string
	Cursor  int64
}

// Next returns what changed since the last call. The first call returns every board's
// head and no presence: a reader takes the current presence from the members list.
// When nothing changed, it waits until something does, the human joins a board, or
// tick fires. On tick it reads once more, so a board joined without a signal is still
// found and a presence that ran out is noticed, and returns what changed (possibly
// nothing) with ticked true.
func (f *HeadFeed) Next(ctx context.Context, tick <-chan time.Time) (u Update, ticked bool, err error) {
	for {
		// The feed ends with the credential it was opened with.
		var cred credentialEnd
		if cred, err = f.s.watchCredential(ctx, f.p); err != nil {
			return Update{}, false, err
		}
		// Watch before reading, so a change between the read and the wait isn't missed.
		cases := []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(tick)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(boardsOfKey(f.p.Human.ID)))},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(cred.changed)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(cred.expires)},
		}
		for id := range f.sent {
			cases = append(cases,
				reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(id))},
				reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(presenceKey(id)))},
				reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(f.s.notify.Watch(readKey(id)))})
		}
		if u, err = f.read(ctx); err != nil || !u.empty() {
			return u, false, err
		}
		switch chosen, _, _ := reflect.Select(cases); chosen {
		case 0:
			return Update{}, false, fmt.Errorf("follow heads: %w", ctx.Err())
		case 1:
			u, err = f.read(ctx)
			return u, true, err
		}
	}
}

// Start reads what the feed begins with: every board's head, and the presence and read
// positions later updates are measured against. Call it once, before Next and before the
// client is told the stream is open, so a change the client makes once it sees the
// stream open is never taken into the starting point and lost.
func (f *HeadFeed) Start(ctx context.Context) (Update, error) { return f.read(ctx) }

// read returns the heads and presence that differ from those last returned and
// remembers them. Boards the human is no longer on are forgotten.
func (f *HeadFeed) read(ctx context.Context) (Update, error) {
	heads, err := f.s.Heads(ctx, f.p)
	if err != nil {
		return Update{}, err
	}
	var u Update
	current := make(map[string]int64, len(heads))
	ids := make([]string, 0, len(heads))
	names := make(map[string]string, len(heads))
	for _, h := range heads {
		current[h.BoardID] = h.Seq
		ids = append(ids, h.BoardID)
		names[h.BoardID] = h.Board
		if seq, ok := f.sent[h.BoardID]; !ok || seq != h.Seq {
			u.Heads = append(u.Heads, h)
		}
	}
	f.sent = current

	presence, reads, err := f.s.presenceOn(ctx, ids, f.p)
	if err != nil {
		return Update{}, err
	}
	for _, id := range ids {
		before, known := f.presence[id]
		for _, agent := range slices.Sorted(maps.Keys(presence[id])) {
			now := presence[id][agent]
			was, ok := before[agent]
			if !ok {
				was = Presence{State: PresenceNoSession}
			}
			if known && was.State != now.State {
				u.Presence = append(u.Presence, PresenceChange{BoardID: id, Board: names[id], Agent: agent, Presence: now})
			}
		}
	}
	f.presence = presence
	for _, id := range ids {
		before, known := f.reads[id]
		for _, agent := range slices.Sorted(maps.Keys(reads[id])) {
			if now := reads[id][agent]; known && now != before[agent] {
				u.Reads = append(u.Reads, ReadChange{BoardID: id, Board: names[id], Agent: agent, Cursor: now})
			}
		}
	}
	f.reads = reads
	return u, nil
}
