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
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Role  *string `json:"role"`
	Owner *string `json:"owner"`
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
}

type wireBoard struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Template  *string               `json:"template"`
	Charter   string                `json:"charter"`
	Roles     map[string]rules.Role `json:"roles"`
	Policy    rules.Policy          `json:"policy"`
	HeadSeq   int64                 `json:"head_seq"`
	CreatedAt string                `json:"created_at"`
	CreatedBy wireMemberRef         `json:"created_by"`
}

type wireMessage struct {
	ID           string            `json:"id"`
	Board        string            `json:"board"`
	Seq          int64             `json:"seq"`
	At           string            `json:"at"`
	From         wireMemberRef     `json:"from"`
	To           []string          `json:"to"`
	Body         string            `json:"body"`
	ReplyTo      *string           `json:"reply_to"`
	ReplyToSeq   *int64            `json:"reply_to_seq"`
	Urgent       bool              `json:"urgent"`
	ExpectsReply bool              `json:"expects_reply"`
	Trust        string            `json:"trust"`
	Redactions   []board.Redaction `json:"redactions"`
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
	return w
}

func boardOf(v board.View) wireBoard {
	b := v.Board
	return wireBoard{
		ID: b.ID, Name: b.Name, Template: b.Template, Charter: b.Charter, Roles: b.Roles, Policy: b.Policy,
		HeadSeq: b.HeadSeq, CreatedAt: b.CreatedAt, CreatedBy: refOf(v.Creator),
	}
}

// trust says how a message's sender relates to its reader: the reader itself, the
// reader's own human, another human, or an agent.
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
	return wireMessage{
		ID: m.ID, Board: boardName, Seq: m.Seq, At: m.At,
		From: wireMemberRef{Name: m.SenderName, Kind: m.SenderKind, Role: m.SenderRole, Owner: m.SenderOwner},
		To:   m.To, Body: m.Body, ReplyTo: m.ReplyTo, ReplyToSeq: m.ReplyToSeq, Urgent: m.Urgent, ExpectsReply: m.ExpectsReply,
		Trust: trust(m, reader), Redactions: m.Redactions,
	}
}

func messagesOf(r board.Reading) []wireMessage {
	out := make([]wireMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		out = append(out, messageOf(m, r.Board.Name, r.Reader))
	}
	return out
}
