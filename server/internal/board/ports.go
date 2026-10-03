package board

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Store runs the domain's reads and writes in transactions.
type Store interface {
	// Read runs fn on a consistent view of the data and always rolls back.
	Read(ctx context.Context, fn func(ReadTx) error) error
	// Write runs fn and commits if it returns nil, rolling back otherwise. Writers run
	// one at a time, so a board's next seq can be read and appended without racing.
	Write(ctx context.Context, fn func(Tx) error) error
}

// ReadTx is everything the domain reads. Lookups of one record return ErrNotFound when
// it doesn't exist.
type ReadTx interface {
	// HumanByTokenDigest finds the human whose login token has this digest.
	HumanByTokenDigest(digest string) (Human, error)
	// HumanCount returns how many humans have a login on this server.
	HumanCount() (int, error)
	// BoardByName finds a board by name.
	BoardByName(name string) (Board, error)
	// BoardByID finds a board by id.
	BoardByID(id string) (Board, error)
	// BoardNameTaken reports whether a board already has this name.
	BoardNameTaken(name string) (bool, error)
	// BoardsOfHuman lists, by name, the boards the human is a human member of.
	BoardsOfHuman(humanID string) ([]Board, error)
	// MemberByTokenDigest finds the agent whose token has this digest.
	MemberByTokenDigest(digest string) (Member, error)
	// HumanMember finds a human's own membership of a board.
	HumanMember(boardID, humanID string) (Member, error)
	// MemberByName finds a member of a board by name.
	MemberByName(boardID, name string) (Member, error)
	// Members lists a board's members in the order they joined.
	Members(boardID string) ([]Member, error)
	// JoinCodeByDigest finds a join code by the digest of the code.
	JoinCodeByDigest(digest string) (JoinCode, error)
	// JoinCodeByID finds a join code by id.
	JoinCodeByID(id string) (JoinCode, error)
	// Events returns up to limit of a board's events after seq, oldest first.
	Events(boardID string, after int64, limit int) ([]events.Event, error)
	// Timeline returns up to q.Limit of the board's messages that reader may see and that
	// match q, oldest first. Reader may see every message when readAll is true, otherwise
	// those it sent or that are addressed to all, to it by name or to its role.
	Timeline(boardID string, reader Member, readAll bool, q TimelineQuery) ([]Message, error)
	// Inbox returns up to limit messages after the reader's cursor that are addressed to
	// it and that it didn't send, oldest first.
	Inbox(reader Member, limit int) ([]Message, error)
	// MessageByID finds a message, with its sender and the seq of the message it replies to.
	MessageByID(id string) (Message, error)
	// MessagesBySeq returns the board's messages among seqs, keyed by seq.
	MessagesBySeq(boardID string, seqs []int64) (map[int64]Message, error)
}

// TimelineQuery says which messages Timeline returns. Zero values don't filter.
type TimelineQuery struct {
	// After and Before bound the seq window, exclusive at both ends.
	After, Before int64
	// Newest fills the page from the newest matching messages instead of the oldest.
	Newest bool
	Limit  int
	// FromID keeps messages sent by this member.
	FromID string
	// SenderRole keeps messages sent by members with this role.
	SenderRole string
	// ToMe keeps messages addressed to the reader (all, its role or @its name) that it
	// didn't send.
	ToMe bool
}

// Tx adds the writes. They are kept only if the Write that runs them commits.
type Tx interface {
	ReadTx
	// InsertHuman adds a human.
	InsertHuman(h Human) error
	// InsertBoard adds a board.
	InsertBoard(b Board) error
	// SetBoardPolicy replaces a board's policy.
	SetBoardPolicy(boardID string, p rules.Policy) error
	// InsertMember adds a member to its board.
	InsertMember(m Member) error
	// SetCursor moves a member's read position forward; it never moves it back.
	SetCursor(memberID string, seq int64) error
	// SetPresence replaces an agent's presence.
	SetPresence(memberID string, p Presence) error
	// InsertJoinCode adds a join code.
	InsertJoinCode(j JoinCode) error
	// RevokeJoinCode marks a join code revoked at a time; a revoked code keeps its first time.
	RevokeJoinCode(id, at string) error
	// AppendEvent adds the next event to its board's log and moves the board's head. It
	// fails, storing nothing, unless the event's seq is exactly one past the head.
	AppendEvent(e events.Event) error
	// InsertMessage adds a message.
	InsertMessage(m Message) error
}

// Notifier wakes readers waiting on a board, or on a human's list of boards. It only
// signals; readers re-read the store. Keys are board ids, or keys the board package makes
// for other things readers wait on; a Notifier treats them all alike.
type Notifier interface {
	// Watch returns a channel closed at the key's next change. Call it before reading,
	// so a change between the read and the wait isn't missed.
	Watch(key string) <-chan struct{}
	// Changed wakes everyone watching the key.
	Changed(key string)
}
