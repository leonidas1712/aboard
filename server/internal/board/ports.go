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
	// AccessKeyByDigest finds an access key by the digest of its secret, whether or not
	// it still works.
	AccessKeyByDigest(digest string) (AccessKey, error)
	// AccessKeyByID finds an access key by id.
	AccessKeyByID(id string) (AccessKey, error)
	// HumanByID finds a human by id.
	HumanByID(id string) (Human, error)
	// HumanByName finds a human by handle.
	HumanByName(name string) (Human, error)
	// HumanCount returns how many people this server has.
	HumanCount() (int, error)
	// ServerInviteByDigest finds a server invite by the digest of its secret.
	ServerInviteByDigest(digest string) (ServerInvite, error)
	// BrowserLoginByDigest finds a browser login by the digest of its token, whether or
	// not it has expired.
	BrowserLoginByDigest(digest string) (BrowserLogin, error)
	// BoardByName finds a board by name.
	BoardByName(name string) (Board, error)
	// BoardByID finds a board by id.
	BoardByID(id string) (Board, error)
	// BoardNameTaken reports whether a board already has this name.
	BoardNameTaken(name string) (bool, error)
	// BoardsOfHuman lists, by name, the boards the human is on: those where their own
	// membership is active.
	BoardsOfHuman(humanID string) ([]Board, error)
	// BoardsSeenBy lists, by name, every open board and every board the human is on.
	BoardsSeenBy(humanID string) ([]Board, error)
	// PrivateBoardsNotOn lists, oldest first, the private boards the human isn't on.
	PrivateBoardsNotOn(humanID string) ([]Board, error)
	// BoardCreation returns who may create boards: CreationMembers unless set.
	BoardCreation() (string, error)
	// WorkingJoinCodes lists a board's join codes that are neither revoked nor expired
	// at now, oldest first.
	WorkingJoinCodes(boardID, now string) ([]JoinCode, error)
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
	// Thread returns up to limit replies after seq in the thread rootID starts that
	// reader may see, oldest first. readAll means as for Timeline.
	Thread(rootID string, reader Member, readAll bool, after int64, limit int) ([]Message, error)
	// ThreadCounts returns, for each of rootIDs whose thread has a reply reader may see,
	// how many such replies it has and when the newest was posted. readAll means as for
	// Timeline.
	ThreadCounts(reader Member, readAll bool, rootIDs []string) (map[string]ThreadCount, error)
	// Threads returns up to limit of the board's threads that have a reply reader may
	// see and whose first message reader may see too, the one with the newest such reply
	// first. Repliers counts only the replies reader may see. readAll means as for
	// Timeline.
	Threads(boardID string, reader Member, readAll bool, limit int) ([]ThreadInfo, error)
	// Reactions returns the reactions to each of messageIDs that has any, oldest first.
	Reactions(messageIDs []string) (map[string][]Reaction, error)
}

// ThreadCount counts the replies in one thread that a reader may see.
type ThreadCount struct {
	Replies int
	LastAt  string
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
	// InsertHuman adds a human, whose handle no other human has.
	InsertHuman(h Human) error
	// InsertAccessKey adds an access key.
	InsertAccessKey(k AccessKey) error
	// NameUnnamedKeys gives every access key with an empty name this name.
	NameUnnamedKeys(name string) error
	// InsertServerInvite adds a server invite.
	InsertServerInvite(i ServerInvite) error
	// UseServerInvite marks an unused invite used at a time by a person. It reports
	// false, changing nothing, when the invite was already used.
	UseServerInvite(id, at, humanID string) (bool, error)
	// InsertBrowserLogin adds a browser login.
	InsertBrowserLogin(l BrowserLogin) error
	// DeleteBrowserLogins removes every browser login of a human and returns how many
	// of them had not expired at now.
	DeleteBrowserLogins(humanID, now string) (int, error)
	// DeleteExpiredBrowserLogins removes the browser logins whose ExpiresAt is at or
	// before now.
	DeleteExpiredBrowserLogins(now string) error
	// InsertBoard adds a board.
	InsertBoard(b Board) error
	// SetBoardPolicy replaces a board's policy.
	SetBoardPolicy(boardID string, p rules.Policy) error
	// SetBoardTitle replaces a board's title; nil removes it.
	SetBoardTitle(boardID string, title *string) error
	// SetBoardVisibility makes a board BoardOpen or BoardPrivate.
	SetBoardVisibility(boardID, visibility string) error
	// SetBoardCreation sets who may create boards.
	SetBoardCreation(v string) error
	// InsertMember adds a member to its board.
	InsertMember(m Member) error
	// SetMemberStatus sets a member's status: StatusActive, StatusLeft or StatusRemoved.
	SetMemberStatus(memberID, status string) error
	// SetMemberAccess sets a person's access on their board; it changes nothing for an
	// agent.
	SetMemberAccess(memberID, access string) error
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
	// InsertMessage adds a message and counts it on its board (MessageCount,
	// LastMessageAt).
	InsertMessage(m Message) error
	// InsertReaction adds a member's reaction to a message, which it hasn't made yet.
	InsertReaction(r Reaction) error
	// DeleteReaction removes a member's reaction to a message, if there is one.
	DeleteReaction(messageID, memberID, name string) error
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
