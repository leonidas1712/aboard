package board

import (
	"context"
	"errors"
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

// Unavailable names only a board previously observed by this feed.
type Unavailable struct {
	BoardID  string
	MemberID *string
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
	// positions is, by board id, the human's own read position and unread count last
	// returned.
	positions map[string]Position
	watches   map[string]headWatch
	resync    map[string]bool
	started   bool
}

type headWatch struct {
	boardID string
	changed <-chan struct{}
}

// FollowHeads starts following the heads of the human's boards. Only humans may.
func (s *Service) FollowHeads(p Principal) (*HeadFeed, error) {
	if err := requireHuman(p); err != nil {
		return nil, err
	}
	return &HeadFeed{
		s: s, p: p, sent: map[string]int64{}, presence: map[string]map[string]Presence{}, reads: map[string]map[string]int64{},
		positions: map[string]Position{},
	}, nil
}

// Update is what changed on a human's boards: heads that moved, agents whose presence
// changed, the human's own agents whose read position moved, and the human's own read
// position or unread count where either changed.
type Update struct {
	Unavailable []Unavailable
	Heads       []Head
	Presence    []PresenceChange
	Reads       []ReadChange
	Unread      []UnreadChange
}

func (u Update) empty() bool {
	return len(u.Unavailable) == 0 && len(u.Heads) == 0 && len(u.Presence) == 0 && len(u.Reads) == 0 && len(u.Unread) == 0
}

// UnreadChange is the human's own read position and unread count on one of their boards,
// sent when a HeadFeed first reads the board and whenever either changes. Only the human
// is told.
type UnreadChange struct {
	BoardID  string
	Board    string // the board's name
	Position Position
}

// ReadChange is the read position of one of the human's agents that moved since a
// HeadFeed last looked: by its own inbox acknowledgement, its owner's delivery daemon's,
// or any other client's. Only the agent's owner is told.
type ReadChange struct {
	BoardID  string
	Board    string // the board's name
	Agent    string
	MemberID string // the agent's seat; Agent is its name now, for display
	Cursor   int64
}

// Next returns what changed since the last call. The first call returns every board's
// head and the human's read position on it, and no presence: a reader takes the current
// presence from the members list.
// When nothing changed, it waits until something does, the human joins a board, or
// tick fires. On tick it reads once more, so a board joined without a signal is still
// found and a presence that ran out is noticed, and returns what changed (possibly
// nothing) with ticked true.
func (f *HeadFeed) Next(ctx context.Context, tick <-chan time.Time) (u Update, ticked bool, err error) {
	if f.watches == nil {
		f.watches = map[string]headWatch{}
		f.resync = map[string]bool{}
		key := boardsOfKey(f.p.Human.ID)
		f.watches[key] = headWatch{changed: f.s.notify.Watch(key)}
	}
	for {
		// The feed ends with the credential it was opened with.
		var cred credentialEnd
		if cred, err = f.s.watchCredential(ctx, f.p); err != nil {
			return Update{}, false, err
		}
		// Retain subscriptions acquired before the last snapshot across Next calls.
		// A commit after that snapshot therefore leaves a closed channel to refresh.
		full := !f.started
		dirty := f.resync
		f.resync = map[string]bool{}
		for key, watch := range f.watches {
			select {
			case <-watch.changed:
				if watch.boardID == "" {
					full = true
				} else {
					dirty[watch.boardID] = true
				}
				watch.changed = f.s.notify.Watch(key)
				f.watches[key] = watch
			default:
			}
		}
		if full || len(dirty) != 0 {
			if full {
				u, err = f.read(ctx)
			} else {
				u, err = f.readBoards(ctx, slices.Sorted(maps.Keys(dirty)))
			}
			f.started = true
			f.followBoards()
			if err != nil || !u.empty() {
				return u, false, err
			}
			if len(f.resync) != 0 {
				continue
			}
		}
		cases := []reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(tick)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(cred.changed)},
			{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(cred.expires)},
		}
		for _, watch := range f.watches {
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(watch.changed)})
		}
		switch chosen, _, _ := reflect.Select(cases); chosen {
		case 0:
			return Update{}, false, fmt.Errorf("follow heads: %w", ctx.Err())
		case 1:
			u, err = f.read(ctx)
			f.followBoards()
			return u, true, err
		}
	}
}

func (f *HeadFeed) followBoards() {
	for key, watch := range f.watches {
		if watch.boardID != "" {
			if _, present := f.sent[watch.boardID]; !present {
				delete(f.watches, key)
			}
		}
	}
	for id := range f.sent {
		for _, key := range []string{id, presenceKey(id), readKey(id, f.p.Human.ID)} {
			if _, known := f.watches[key]; !known {
				f.watches[key] = headWatch{boardID: id, changed: f.s.notify.Watch(key)}
				// A new board's first snapshot preceded its subscription. Re-read it
				// after subscribing before waiting, to cover that startup window.
				f.resync[id] = true
			}
		}
	}
}

// Start reads the stream's starting point before HTTP success is reported, so a
// change made after the client sees success is never folded into that starting point.
func (f *HeadFeed) Start(ctx context.Context) (Update, error) { return f.read(ctx) }

// read returns the heads and presence that differ from those last returned and
// remembers them. Boards the human is no longer on are forgotten.
func (f *HeadFeed) read(ctx context.Context) (Update, error) {
	return f.readBoards(ctx, nil)
}

func (f *HeadFeed) readBoards(ctx context.Context, selected []string) (Update, error) {
	var heads []Head
	var err error
	if selected == nil {
		heads, err = f.s.Heads(ctx, f.p)
	} else {
		err = f.s.st.Read(ctx, func(tx ReadTx) error {
			if err := stillValid(tx, f.p, stamp(f.s.clk.Now())); err != nil {
				return err
			}
			for _, id := range selected {
				b, err := tx.BoardByID(id)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if b.Lifecycle == LifecycleDeleted {
					continue
				}
				me, err := tx.HumanMember(id, f.p.Human.ID)
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if me.Status == StatusActive {
					heads = append(heads, Head{BoardID: id, Board: b.Name, Seq: b.HeadSeq})
				}
			}
			return nil
		})
	}
	if err != nil {
		return Update{}, err
	}
	ids := make([]string, 0, len(heads))
	for _, h := range heads {
		ids = append(ids, h.BoardID)
	}
	presence, reads, positions, seats, err := f.s.presenceOn(ctx, ids, f.p)
	if err != nil {
		return Update{}, err
	}
	var u Update
	current := make(map[string]int64, len(heads))
	names := make(map[string]string, len(heads))
	ids = ids[:0]
	for _, h := range heads {
		// The second transaction rechecks access before exposing names or counts.
		if _, authorized := positions[h.BoardID]; !authorized {
			continue
		}
		current[h.BoardID] = h.Seq
		ids = append(ids, h.BoardID)
		names[h.BoardID] = h.Board
		if seq, ok := f.sent[h.BoardID]; !ok || seq != h.Seq {
			u.Heads = append(u.Heads, h)
		}
	}
	previous := f.sent
	if selected != nil {
		previous = map[string]int64{}
		for _, id := range selected {
			if seq, known := f.sent[id]; known {
				previous[id] = seq
			}
		}
	}
	for id := range previous {
		if _, ok := current[id]; ok {
			continue
		}
		u.Unavailable = append(u.Unavailable, Unavailable{BoardID: id})
		delete(f.sent, id)
		delete(f.presence, id)
		delete(f.reads, id)
		delete(f.positions, id)
	}
	if selected == nil {
		f.sent = current
	} else {
		maps.Copy(f.sent, current)
	}

	for _, id := range ids {
		if pos, ok := positions[id]; ok {
			if before, known := f.positions[id]; !known || before != pos {
				u.Unread = append(u.Unread, UnreadChange{BoardID: id, Board: names[id], Position: pos})
			}
		}
	}
	if selected == nil {
		f.positions = positions
	} else {
		maps.Copy(f.positions, positions)
	}
	for _, id := range ids {
		before, known := f.presence[id]
		for _, agent := range slices.Sorted(maps.Keys(presence[id])) {
			now := presence[id][agent]
			was, ok := before[agent]
			if !ok {
				was = Presence{State: PresenceNoSession}
			}
			if known && (was.State != now.State || was.Delivery != now.Delivery) {
				u.Presence = append(u.Presence, PresenceChange{BoardID: id, Board: names[id], Agent: agent, MemberID: seats[id][agent], Presence: now})
			}
		}
	}
	if selected == nil {
		f.presence = presence
	} else {
		maps.Copy(f.presence, presence)
	}
	for _, id := range ids {
		before, known := f.reads[id]
		for _, agent := range slices.Sorted(maps.Keys(reads[id])) {
			if now := reads[id][agent]; known && now != before[agent] {
				u.Reads = append(u.Reads, ReadChange{BoardID: id, Board: names[id], Agent: agent, MemberID: seats[id][agent], Cursor: now})
			}
		}
	}
	if selected == nil {
		f.reads = reads
	} else {
		maps.Copy(f.reads, reads)
	}
	return u, nil
}
