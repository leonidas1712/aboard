package board

import (
	"errors"

	"github.com/leonidas1712/aboard/server/internal/rules"
)

// ErrNotFound is returned by a Store when a looked-up record doesn't exist.
var ErrNotFound = errors.New("not found")

// Human is a person on this server. ID is permanent; Name is their handle, unique on the
// server and their member name on boards.
type Human struct {
	ID          string
	Name        string
	DisplayName *string
	Role        string // ServerAdmin or ServerMember
	CreatedAt   string
}

// A person's role on the server.
const (
	ServerAdmin  = "admin"
	ServerMember = "member"
)

// AccessKey is a person's credential: a named secret kept by its keyed digest. Agent
// tokens and browser logins made with a key stop working when it does.
type AccessKey struct {
	ID        string
	HumanID   string
	Name      string
	Digest    string
	CreatedAt string
	ExpiresAt *string // nil: it doesn't expire
	RevokedAt *string
	// LastUsedAt is when the key, or a browser login or agent token it started, was last
	// used, recorded at most once a minute; nil before its first use.
	LastUsedAt *string
	// IdleSeconds, when set, makes the key expire only once unused: each recorded use
	// moves ExpiresAt this many seconds past the use.
	IdleSeconds *int64
}

// KeyUsage is an access key with what still depends on it: the browser logins it started
// that haven't ended, and the agents whose tokens came from it.
type KeyUsage struct {
	AccessKey
	BrowserSessions int
	AgentSeats      int
}

// ServerInvite lets one new person onto the server, as a member, once, before it
// expires. It is kept by the keyed digest of its secret.
type ServerInvite struct {
	ID        string
	Digest    string
	CreatedBy string // the admin's person id
	CreatedAt string
	ExpiresAt string
	UsedAt    *string
	UsedBy    *string
}

// MachineRequest is a new machine asking for an access key, for a person to approve
// with its short code. It is kept by the keyed digests of the short code and of the long
// secret the machine collects its key with, never by either secret.
type MachineRequest struct {
	ID           string
	CodeDigest   string
	SecretDigest string
	Label        string // the name the machine gave itself; it names the key
	// Handle names the person the request is for: only their own key may decide it. It
	// may name nobody, and then nobody can.
	Handle        string
	RequestedFrom string // the client address the request came from
	CreatedAt     string
	ExpiresAt     string
	State         string // a MachineRequest state
	// DecidedBy is the person who approved or refused it, and DecidedKey the key they
	// did it with, which must still work when the machine collects its key.
	DecidedBy  *string
	DecidedKey *string
	DecidedAt  *string
	// KeyID is the key the machine collected, once it has.
	KeyID *string
	// Polls counts the machine's collection attempts while it waited.
	Polls int
}

// MachineRequest states. A request is pending until a person approves or refuses it; an
// approved one is collected once.
const (
	MachinePending   = "pending"
	MachineApproved  = "approved"
	MachineRefused   = "refused"
	MachineCollected = "collected"
)

// BrowserLogin is a browser token, kept by the digest of the token and never the token
// itself. Like a read cursor it is bookkeeping, not part of any board's record.
type BrowserLogin struct {
	TokenDigest string
	HumanID     string
	// KeyID is the access key that started the login, which it never outlives.
	KeyID     string
	CreatedAt string
	ExpiresAt string
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
	// Visibility is BoardOpen or BoardPrivate: who can see the board at all.
	Visibility string
}

// Who can see a board. An open board is seen by every person on the server, who may
// join it; a private one only by the people on it.
const (
	BoardOpen    = "open"
	BoardPrivate = "private"
)

// Who may create boards on the server.
const (
	CreationMembers = "members"
	CreationAdmins  = "admins"
)

// Whether a member is on its board. A person who left or was removed keeps their row,
// so their messages stay theirs and they come back under the same name.
const (
	StatusActive  = "active"
	StatusLeft    = "left"
	StatusRemoved = "removed"
)

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
	// KeyID is the access key the agent's token came from, which it never outlives; nil
	// for a person.
	KeyID    *string
	Access   string // rules.AccessAdmin or rules.AccessMember for a person, empty for an agent
	Status   string
	Cursor   int64
	JoinedAt string
	// Presence is what was last reported for an agent; read it with CurrentPresence.
	Presence Presence
	// Delivery is an agent's delivery mode as its person set it.
	Delivery DeliverySetting
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
