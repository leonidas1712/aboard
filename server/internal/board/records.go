package board

import (
	"errors"

	"github.com/leonidas1712/aboard/server/internal/rules"
)

// ErrNotFound is returned by a Store when a looked-up record doesn't exist.
var ErrNotFound = errors.New("not found")

// Human is a person with a login on this server.
type Human struct {
	ID          string
	Name        string
	TokenDigest string
	CreatedAt   string
}

// BrowserLogin is a browser token, kept by the digest of the token and never the token
// itself. Like a read cursor it is bookkeeping, not part of any board's record.
type BrowserLogin struct {
	TokenDigest string
	HumanID     string
	CreatedAt   string
	ExpiresAt   string
}

// Board is a board's current state.
type Board struct {
	ID   string
	Name string
	// Title is free text people read beside the name; nil when the board has none.
	Title    *string
	Template *string
	Charter  string
	Roles    map[string]rules.Role
	Policy   rules.Policy
	HeadSeq  int64
	HeadHash string
	// MessageCount and LastMessageAt describe the board's messages; the store keeps
	// them as messages are inserted. LastMessageAt is nil before the first message.
	MessageCount  int64
	LastMessageAt *string
	CreatedAt     string
	CreatedBy     string // member id of the creating human
}

// Member is a human or agent on a board.
type Member struct {
	ID          string
	BoardID     string
	Name        string
	Kind        string
	Role        *string
	HumanID     string // the human, or the agent's owner
	Owner       *string
	Harness     *string
	TokenDigest *string
	Access      string // rules.AccessAdmin or rules.AccessMember for a person, empty for an agent
	Status      string
	Cursor      int64
	JoinedAt    string
	// Presence is what was last reported for an agent; read it with CurrentPresence.
	Presence Presence
}

// Rules returns what the rules package needs to know about the member.
func (m Member) Rules() rules.Member {
	r := rules.Member{ID: m.ID, Name: m.Name, Kind: m.Kind, HumanID: m.HumanID, Access: m.Access}
	if m.Role != nil {
		r.Role = *m.Role
	}
	return r
}

// JoinCode is a code that lets sessions join a board in one role.
type JoinCode struct {
	ID         string
	BoardID    string
	CodeDigest string
	Role       string
	ExpiresAt  string
	CreatedAt  string
	CreatedBy  string
	RevokedAt  *string
}

// Redaction records how many secrets of one kind were replaced in a message. Its JSON
// form is part of the message.posted event payload, which is hashed into the chain, so
// the field names are fixed.
type Redaction struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Message is a stored message with its sender.
type Message struct {
	ID         string
	BoardID    string
	Seq        int64
	At         string
	SenderID   string
	To         []string
	Body       string
	ReplyTo    *string
	ReplyToSeq *int64
	// ReplyToFrom is the name of the member who sent the message this one replies to;
	// the store fills it when it reads a message.
	ReplyToFrom *string
	// ThreadRoot is the first message of the reply's thread; nil for a message that
	// replies to nothing. The store fills ThreadRootSeq when it reads a message.
	ThreadRoot    *string
	ThreadRootSeq *int64
	// ReplyCount and LastReplyAt describe the thread a message starts, counting only the
	// replies its reader may see. The service fills them for each reader.
	ReplyCount   int
	LastReplyAt  *string
	Urgent       bool
	ExpectsReply bool
	Redactions   []Redaction
	// Reactions are the reactions on the message as its reader sees them, in the set's
	// order. The service fills them for each reader.
	Reactions []ReactionCount

	// Sender fields, filled in by the store when it reads a message.
	SenderName    string
	SenderKind    string
	SenderRole    *string
	SenderOwner   *string
	SenderHuman   string
	SenderHarness *string
	// AgentOwners is how many people have agents on the board, when the message was read.
	AgentOwners int
}

// Reaction is one member's reaction to a message: a read model kept from the
// reaction.added and reaction.removed events.
type Reaction struct {
	MessageID string
	MemberID  string
	// Name is the reaction's name in the set, such as "thumbsup".
	Name string
	At   string
	// MemberName is filled in by the store when it reads a reaction.
	MemberName string
}

// ThreadInfo is a thread a reader may see, as the store lists them: its first message
// and the names of the members who replied in it, in the order they first replied.
type ThreadInfo struct {
	RootID   string
	Repliers []string
}
