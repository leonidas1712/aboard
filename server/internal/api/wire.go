package api

import (
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// The wire* types have exactly the JSON shape spec/openapi.yaml gives each object. They
// are converted into the generated types with convert, which keeps union types (such as
// permission grants and events) intact.

type wireMemberRef struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Role    *string `json:"role"`
	Owner   *string `json:"owner"`
	Harness *string `json:"harness,omitempty"`
}

type wireMember struct {
	ID       string  `json:"id"`
	Board    string  `json:"board"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Role     *string `json:"role"`
	Owner    *string `json:"owner"`
	Harness  *string `json:"harness"`
	Access   *string `json:"access"`
	Status   string  `json:"status"`
	JoinedAt string  `json:"joined_at"`
	// Presence and PresenceSince are null for people.
	Presence      *string `json:"presence"`
	PresenceSince *string `json:"presence_since"`
	// Delivery is the agent's delivery mode as last reported; null for people.
	Delivery *string `json:"delivery"`
}

type wireBoard struct {
	ID       string                `json:"id"`
	Name     string                `json:"name"`
	Title    *string               `json:"title"`
	Template *string               `json:"template"`
	Charter  string                `json:"charter"`
	Roles    map[string]rules.Role `json:"roles"`
	Policy   rules.Policy          `json:"policy"`
	HeadSeq  int64                 `json:"head_seq"`
	// MessageCount and LastMessageAt are null for a reader who may not see them.
	MessageCount  *int64        `json:"message_count"`
	LastMessageAt *string       `json:"last_message_at"`
	CreatedAt     string        `json:"created_at"`
	CreatedBy     wireMemberRef `json:"created_by"`
	Visibility    string        `json:"visibility"`
	OnBoard       bool          `json:"on_board"`
	// ReadUpTo and Unread are the caller's read position, left out when they aren't on
	// the board.
	ReadUpTo *int64 `json:"read_up_to,omitempty"`
	Unread   *int64 `json:"unread,omitempty"`
}

type wireMessage struct {
	ID            string            `json:"id"`
	Board         string            `json:"board"`
	Seq           int64             `json:"seq"`
	At            string            `json:"at"`
	From          wireMemberRef     `json:"from"`
	To            []string          `json:"to"`
	Body          string            `json:"body"`
	ReplyTo       *string           `json:"reply_to"`
	ReplyToSeq    *int64            `json:"reply_to_seq"`
	ReplyToFrom   *string           `json:"reply_to_from"`
	ThreadRoot    *string           `json:"thread_root"`
	ThreadRootSeq *int64            `json:"thread_root_seq"`
	ReplyCount    int               `json:"reply_count"`
	LastReplyAt   *string           `json:"last_reply_at"`
	Urgent        bool              `json:"urgent"`
	ExpectsReply  bool              `json:"expects_reply"`
	Sender        string            `json:"sender"`
	ShowOwner     bool              `json:"show_owner"`
	Trust         string            `json:"trust"`
	Redactions    []board.Redaction `json:"redactions"`
	Reactions     []wireReaction    `json:"reactions"`
}

type wireReaction struct {
	Name  string   `json:"name"`
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	By    []string `json:"by"`
	Mine  bool     `json:"mine"`
}

type wireJoinCode struct {
	ID        string        `json:"id"`
	Code      string        `json:"code,omitempty"`
	JoinLine  string        `json:"join_line,omitempty"`
	Board     string        `json:"board"`
	Role      string        `json:"role"`
	ExpiresAt string        `json:"expires_at"`
	CreatedAt string        `json:"created_at"`
	CreatedBy wireMemberRef `json:"created_by"`
	RevokedAt *string       `json:"revoked_at"`
}

// convert re-decodes v's JSON into a T.
func convert[T any](v any) (T, error) {
	var out T
	b, err := json.Marshal(v)
	if err != nil {
		return out, fmt.Errorf("encode %T: %w", v, err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("decode into %T: %w", out, err)
	}
	return out, nil
}

func refOf(m board.Member) wireMemberRef {
	return wireMemberRef{Name: m.Name, Kind: m.Kind, Role: m.Role, Owner: m.Owner}
}

func memberOf(m board.Member, boardName string) wireMember {
	w := wireMember{
		ID: m.ID, Board: boardName, Name: m.Name, Kind: m.Kind, Role: m.Role, Owner: m.Owner,
		Harness: m.Harness, Status: m.Status, JoinedAt: m.JoinedAt,
	}
	if m.Access != "" {
		w.Access = &m.Access
	}
	if m.Kind == "agent" {
		state := m.Presence.State
		if state == "" {
			state = board.PresenceNoSession
		}
		w.Presence, w.PresenceSince, w.Delivery = &state, nullable(m.Presence.Since), nullable(m.Presence.Delivery)
	}
	return w
}

// boardOf is a board as reader p sees it.
func boardOf(v board.View, p board.Principal) wireBoard {
	b := v.Board
	w := wireBoard{
		ID: b.ID, Name: b.Name, Title: b.Title, Template: b.Template, Charter: b.Charter, Roles: b.Roles, Policy: b.Policy,
		HeadSeq: b.HeadSeq, CreatedAt: b.CreatedAt, CreatedBy: refOf(v.Creator), Visibility: b.Visibility, OnBoard: v.OnBoard,
	}
	if v.ShowsCounts(p) {
		w.MessageCount, w.LastMessageAt = &b.MessageCount, b.LastMessageAt
	}
	return withPosition(w, v)
}

// sender is the sender label: who sent a message relative to its reader. A person
// counts as their own owner, so their agents are owner_agent to them.
func sender(m board.Message, reader board.Member) string {
	switch {
	case m.SenderID == reader.ID:
		return "self"
	case m.SenderKind == "human" && reader.Kind == "agent" && m.SenderHuman == reader.HumanID:
		return "owner"
	case m.SenderKind == "human":
		return "other_person"
	case m.SenderHuman == reader.HumanID:
		return "owner_agent"
	default:
		return "other_agent"
	}
}

// trust is the older form of the sender label, kept in the API for clients that read
// it: the reader itself, the reader's own human, another human, or any agent.
func trust(m board.Message, reader board.Member) string {
	switch {
	case m.SenderID == reader.ID:
		return "self"
	case m.SenderKind == "human" && reader.Kind == "agent" && m.SenderHuman == reader.HumanID:
		return "owner"
	case m.SenderKind == "human":
		return "human"
	default:
		return "peer"
	}
}

func messageOf(m board.Message, boardName string, reader board.Member) wireMessage {
	reactions := make([]wireReaction, 0, len(m.Reactions))
	for _, r := range m.Reactions {
		reactions = append(reactions, wireReaction{Name: r.Name, Emoji: r.Emoji, Count: len(r.By), By: r.By, Mine: r.Mine})
	}
	return wireMessage{
		ID: m.ID, Board: boardName, Seq: m.Seq, At: m.At,
		From: wireMemberRef{Name: m.SenderName, Kind: m.SenderKind, Role: m.SenderRole, Owner: m.SenderOwner, Harness: m.SenderHarness},
		To:   m.To, Body: m.Body, ReplyTo: m.ReplyTo, ReplyToSeq: m.ReplyToSeq, ReplyToFrom: m.ReplyToFrom,
		ThreadRoot: m.ThreadRoot, ThreadRootSeq: m.ThreadRootSeq, ReplyCount: m.ReplyCount, LastReplyAt: m.LastReplyAt,
		Urgent: m.Urgent, ExpectsReply: m.ExpectsReply,
		Sender: sender(m, reader), ShowOwner: m.AgentOwners > 1, Trust: trust(m, reader), Redactions: m.Redactions,
		Reactions: reactions,
	}
}

// messagesOf converts a reading, hiding senders' harnesses from an agent when the
// board's policy says so.
func messagesOf(r board.Reading) []wireMessage {
	hide := r.Reader.Kind == "agent" && !r.Board.Policy.ShowHarness
	out := make([]wireMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		if hide {
			m.SenderHarness = nil
		}
		out = append(out, messageOf(m, r.Board.Name, r.Reader))
	}
	return out
}
