package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Redaction records how many secrets of one kind were replaced in a message.
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

	// Sender fields, filled in by queries.
	SenderName  string
	SenderKind  string
	SenderRole  *string
	SenderOwner *string
	SenderHuman string
}

// InsertMessage adds a message.
func (t *Tx) InsertMessage(m Message) error {
	to, err := json.Marshal(m.To)
	if err != nil {
		return fmt.Errorf("encode targets: %w", err)
	}
	if m.Redactions == nil {
		m.Redactions = []Redaction{}
	}
	red, err := json.Marshal(m.Redactions)
	if err != nil {
		return fmt.Errorf("encode redactions: %w", err)
	}
	return t.exec("INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, urgent, expects_reply, redactions_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Seq, m.At, m.SenderID, string(to), m.Body, m.ReplyTo, m.Urgent, m.ExpectsReply, string(red))
}

const messageSelect = `SELECT m.id, m.board_id, m.seq, m.at, m.sender_id, m.to_json, m.body, m.reply_to,
	m.urgent, m.expects_reply, m.redactions_json, s.name, s.kind, s.role, s.owner, s.human_id, r.seq
	FROM messages m JOIN members s ON s.id = m.sender_id LEFT JOIN messages r ON r.id = m.reply_to`

// addressedTo is a SQL condition matching messages whose targets include all, @name or
// role:R. It takes the @name and role:R strings as parameters.
const addressedTo = `EXISTS (SELECT 1 FROM json_each(m.to_json) WHERE value IN ('all', ?, ?))`

func (t *Tx) queryMessages(where string, args ...any) ([]Message, error) {
	rows, err := t.tx.QueryContext(t.ctx, messageSelect+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []Message
	for rows.Next() {
		var m Message
		var to, red string
		if err := rows.Scan(&m.ID, &m.BoardID, &m.Seq, &m.At, &m.SenderID, &to, &m.Body, &m.ReplyTo,
			&m.Urgent, &m.ExpectsReply, &red, &m.SenderName, &m.SenderKind, &m.SenderRole, &m.SenderOwner, &m.SenderHuman, &m.ReplyToSeq); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(to), &m.To); err != nil {
			return nil, fmt.Errorf("message %s targets: %w", m.ID, err)
		}
		if err := json.Unmarshal([]byte(red), &m.Redactions); err != nil {
			return nil, fmt.Errorf("message %s redactions: %w", m.ID, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func targetsOf(reader Member) (name, role string) {
	name = "@" + reader.Name
	if reader.Role != nil && *reader.Role != "" {
		role = "role:" + *reader.Role
	}
	return name, role
}

// Timeline returns messages after seq that reader may see, oldest first. When readAll is
// true (open visibility, or a human reader) that is every message.
func (t *Tx) Timeline(boardID string, reader Member, readAll bool, after int64, limit int) ([]Message, error) {
	name, role := targetsOf(reader)
	return t.queryMessages("m.board_id = ? AND m.seq > ? AND (? OR m.sender_id = ? OR "+addressedTo+") ORDER BY m.seq LIMIT ?",
		boardID, after, readAll, reader.ID, name, role, limit)
}

// Inbox returns messages after the reader's cursor that are addressed to it and that it
// didn't send, oldest first.
func (t *Tx) Inbox(reader Member, limit int) ([]Message, error) {
	name, role := targetsOf(reader)
	return t.queryMessages("m.board_id = ? AND m.seq > ? AND m.sender_id <> ? AND "+addressedTo+" ORDER BY m.seq LIMIT ?",
		reader.BoardID, reader.Cursor, reader.ID, name, role, limit)
}

// MessageByID finds a message.
func (t *Tx) MessageByID(id string) (Message, error) {
	ms, err := t.queryMessages("m.id = ?", id)
	if err != nil {
		return Message{}, err
	}
	if len(ms) == 0 {
		return Message{}, ErrNotFound
	}
	return ms[0], nil
}

// MessagesBySeq returns the messages among seqs, keyed by seq.
func (t *Tx) MessagesBySeq(boardID string, seqs []int64) (map[int64]Message, error) {
	out := map[int64]Message{}
	if len(seqs) == 0 {
		return out, nil
	}
	args := []any{boardID}
	for _, s := range seqs {
		args = append(args, s)
	}
	ms, err := t.queryMessages("m.board_id = ? AND m.seq IN (?"+strings.Repeat(", ?", len(seqs)-1)+")", args...)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		out[m.Seq] = m
	}
	return out, nil
}

// SavedResponse is a stored answer to an idempotent write.
type SavedResponse struct {
	RequestHash string
	Status      int
	ContentType string
	Body        []byte
}

// SavedResponse finds the response saved for an idempotency key, or ErrNotFound.
func (t *Tx) SavedResponse(scope, key string) (SavedResponse, error) {
	var r SavedResponse
	err := t.queryRow("SELECT request_hash, status, content_type, body FROM idempotency WHERE scope = ? AND key = ?", scope, key).
		Scan(&r.RequestHash, &r.Status, &r.ContentType, &r.Body)
	return r, notFound(err)
}

// SaveResponse stores the response to an idempotent write. If one is already saved for
// the key, the first one is kept.
func (t *Tx) SaveResponse(scope, key string, r SavedResponse, at string) error {
	return t.exec("INSERT INTO idempotency (scope, key, request_hash, status, content_type, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
		scope, key, r.RequestHash, r.Status, r.ContentType, r.Body, at)
}
