package board

import (
	"errors"

	"github.com/leonidas1712/aboard/server/internal/rules"
)

// ErrNotFound is returned by a Store when a looked-up record doesn't exist.
var ErrNotFound = errors.New("not found")

// Human is a person on this server. ID is permanent; Name is their handle, unique among
// the people still on the server and their member name on boards.
type Human struct {
	MidturnPolicy string
	ID            string
	Name          string
	DisplayName   *string
	Role          string // ServerAdmin, ServerMember or ServerGuest
	CreatedAt     string
	// RemovedAt is when an admin removed the person from the server, and RemovedBy that
	// admin's id; nil while they are on it. A removed person's handle is free again.
	RemovedAt *string
	RemovedBy *string
}

// A person's role on the server. An admin manages the server's people; a member sees its
// open boards and the private ones they are on; a guest came in through a guest code and
// reaches only the boards guest codes brought them onto, through the agent each made.
const (
	ServerAdmin  = "admin"
	ServerMember = "member"
	ServerGuest  = "guest"
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
	// Delegations counts the machine delegations made with the key that haven't ended;
	// they work only while the key does.
	Delegations int
}

// Delegation is a machine's delegation: made with one of a person's access keys, it
// lists that person's boards and gives the sessions its holder vouches for a seat on
// them. It is kept by the keyed digest of its token. It works while it hasn't ended
// (EndedAt nil), its key works and its person is on the server.
type Delegation struct {
	ID        string
	KeyID     string
	HumanID   string
	Name      string
	Digest    string
	CreatedAt string
	// EndedAt is when another delegation with the same key and name replaced it.
	EndedAt *string
}

// ServerInvite lets one new person onto the server, as a member, once, before it
// expires. It is kept by the keyed digest of its secret.
type ServerInvite struct {
	SuggestedHandle             string
	PairingRequestID            string
	ParentKeyID, IssuingAgentID string
	Authorization               *AdminAuthorization
	RevokedAt                   *string
	Boards                      []string
	ID                          string
	Digest                      string
	CreatedBy                   string // the admin's person id
	CreatedAt                   string
	ExpiresAt                   string
	UsedAt                      *string
	UsedBy                      *string
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
	// ID names the login (ses_…) so its person can list and end it without its secret.
	ID          string
	TokenDigest string
	HumanID     string
	// KeyID is the access key that started the login, which it never outlives.
	KeyID string
	// StartedWith is how the login started: SessionFromLoginCode or SessionFromKey.
	StartedWith string
	CreatedAt   string
	ExpiresAt   string
}

// Board is a board's current state.
type Board struct {
	TaskPrefix *string
	TasksOpen  int64
	ID         string
	Name       string
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
	Visibility      string
	Lifecycle       string
	AgentsAddPeople bool
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
	Location        *AgentLocation
	MidturnOverride *string
	DisplayName     *string
	CurrentTask     *TaskRef
	ID              string
	BoardID         string
	Name            string
	Kind            string
	Role            *string
	HumanID         string // the human, or the agent's owner
	Owner           *string
	Harness         *string
	TokenDigest     *string
	// KeyID is the access key the agent's token came from, which it never outlives; nil
	// for a person, and for a guest's agent, which has no key behind it.
	KeyID    *string
	Access   string // rules.AccessAdmin or rules.AccessMember for a person, empty for an agent
	Status   string
	Cursor   int64
	JoinedAt string
	// Presence is what was last reported for an agent; read it with CurrentPresence.
	Presence Presence
	// PersonRole is the server role of the member's person, or an agent's owner, as the
	// store read it with the member.
	PersonRole string
	// Delivery is an agent's delivery mode as its person set it.
	Delivery DeliverySetting
	// Session is the harness session an agent was made for (`<harness>:<id>`), when its
	// join sent one; never shown or recorded on the board.
	Session *string
	// RemovedAt and RemovedBy say when a removed agent was removed and by whom
	// (RemovedByPerson, RemovedByOwner or RemovedByAdmin); nil for agents removed
	// before this was kept, and for everyone else.
	RemovedAt *string
	RemovedBy *string
}

// Who removed an agent: its own person (removing it, or leaving the board), one of the
// board's owners, a server admin, or the agent itself, leaving.
const (
	RemovedByPerson = "person"
	RemovedByOwner  = "board_owner"
	RemovedByAdmin  = "admin"
	RemovedBySelf   = "self"
)

// Rules returns what the rules package needs to know about the member.
func (m Member) Rules() rules.Member {
	r := rules.Member{ID: m.ID, Name: m.Name, Kind: m.Kind, HumanID: m.HumanID, Access: m.Access}
	if m.Role != nil {
		r.Role = *m.Role
	}
	return r
}

// JoinCode is a code that lets sessions join a board in one role: a pairing code, which
// its maker's own sessions redeem until it expires, or a guest code, which lets the one
// guest it names onto the board, once.
type JoinCode struct {
	ID         string
	BoardID    string
	CodeDigest string
	Role       string
	ExpiresAt  string
	CreatedAt  string
	CreatedBy  string
	RevokedAt  *string
	Kind       string  // CodePairing or CodeGuest
	Guest      *string // the guest's handle, for a guest code
	GuestID    *string // an existing guest's permanent person id at issuance
	UsedAt     *string // when a guest code was used
	UsedBy     *string // the member id of the agent a guest code made
}

// Kinds of join code.
const (
	CodePairing = "pairing"
	CodeGuest   = "guest"
)

// Redaction records how many secrets of one kind were replaced in a message. Its JSON
// form is part of the message.posted event payload, which is hashed into the chain, so
// the field names are fixed.
type Redaction struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Mention is one member a message mentions, as the server resolved it when the message
// was posted. Its JSON form is part of the message.posted event payload, which is hashed
// into the chain, so the field names are fixed.
type Mention struct {
	MemberID string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	// Text is the mention as written: "@codex" or "@role:reviewer".
	Text string `json:"text"`
	// Wakes is true when the mention counts as addressing the agent.
	Wakes bool `json:"wakes"`
	// Reason says why an agent's mention doesn't wake it: MentionLimit or
	// MentionCannotRead. Nil otherwise.
	Reason *string `json:"reason"`
}

// Why a mention of an agent doesn't wake it.
const (
	// MentionLimit: the message mentions more agents than MaxMentionWakes.
	MentionLimit = "limit"
	// MentionCannotRead: the agent may not read the message, and a mention grants no
	// access.
	MentionCannotRead = "cannot_read"
)

// Message is a stored message with its sender.
type Message struct {
	MidturnPeerSenderID *string
	Files               []FileRef
	Ask                 *Ask
	Answer              *Answer
	About               []TaskTag
	ID                  string
	BoardID             string
	Seq                 int64
	At                  string
	SenderID            string
	To                  []string
	Body                string
	ReplyTo             *string
	ReplyToSeq          *int64
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
	// Recipients are the member ids the message was addressed to when it was posted, in
	// the order it named them; nil for a message to all or a legacy message whose
	// recipients were not recorded. To distinguishes those cases.
	Recipients []string
	// Mentions are the members the body mentions, each once, in the order first mentioned.
	Mentions []Mention
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

// TaskTag records why a message is linked to a task.
type TaskTag struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
	How string `json:"how"`
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
