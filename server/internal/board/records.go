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

// Board is a board's current state.
type Board struct {
	ID        string
	Name      string
	Template  *string
	Charter   string
	Roles     map[string]rules.Role
	Policy    rules.Policy
	HeadSeq   int64
	HeadHash  string
	CreatedAt string
	CreatedBy string // member id of the creating human
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
	Status      string
	Cursor      int64
	JoinedAt    string
}

// Rules returns what the rules package needs to know about the member.
func (m Member) Rules() rules.Member {
	r := rules.Member{ID: m.ID, Name: m.Name, Kind: m.Kind}
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
	ID           string
	BoardID      string
	Seq          int64
	At           string
	SenderID     string
	To           []string
	Body         string
	ReplyTo      *string
	ReplyToSeq   *int64
	Urgent       bool
	ExpectsReply bool
	Redactions   []Redaction

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
