package sqlite

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// InsertMessage adds a message.
func (t *tx) InsertMessage(m board.Message) error {
	to, err := json.Marshal(m.To)
	if err != nil {
		return fmt.Errorf("encode targets: %w", err)
	}
	if m.Redactions == nil {
		m.Redactions = []board.Redaction{}
	}
	red, err := json.Marshal(m.Redactions)
	if err != nil {
		return fmt.Errorf("encode redactions: %w", err)
	}
	return t.exec("INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, urgent, expects_reply, redactions_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Seq, m.At, m.SenderID, string(to), m.Body, m.ReplyTo, m.Urgent, m.ExpectsReply, string(red))
}

const messageSelect = `SELECT m.id, m.board_id, m.seq, m.at, m.sender_id, m.to_json, m.body, m.reply_to,
	m.urgent, m.expects_reply, m.redactions_json, s.name, s.kind, s.role, s.owner, s.human_id, s.harness, r.seq,
	(SELECT COUNT(DISTINCT o.human_id) FROM members o WHERE o.board_id = m.board_id AND o.kind = 'agent')
	FROM messages m JOIN members s ON s.id = m.sender_id LEFT JOIN messages r ON r.id = m.reply_to`

// addressedTo is a SQL condition matching messages whose targets include all, @name or
// role:R. It takes the @name and role:R strings as parameters.
const addressedTo = `EXISTS (SELECT 1 FROM json_each(m.to_json) WHERE value IN ('all', ?, ?))`

func (t *tx) queryMessages(where string, args ...any) ([]board.Message, error) {
	rows, err := t.tx.QueryContext(t.ctx, messageSelect+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.Message
	for rows.Next() {
		var m board.Message
		var to, red string
		if err := rows.Scan(&m.ID, &m.BoardID, &m.Seq, &m.At, &m.SenderID, &to, &m.Body, &m.ReplyTo,
			&m.Urgent, &m.ExpectsReply, &red, &m.SenderName, &m.SenderKind, &m.SenderRole, &m.SenderOwner, &m.SenderHuman, &m.SenderHarness, &m.ReplyToSeq, &m.AgentOwners); err != nil {
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

func targetsOf(reader board.Member) (name, role string) {
	name = "@" + reader.Name
	if reader.Role != nil && *reader.Role != "" {
		role = "role:" + *reader.Role
	}
	return name, role
}

// Timeline returns messages that reader may see and that match q, oldest first. When
// readAll is true (open visibility, or a human reader) reader may see every message.
func (t *tx) Timeline(boardID string, reader board.Member, readAll bool, q board.TimelineQuery) ([]board.Message, error) {
	name, role := targetsOf(reader)
	where := []string{"m.board_id = ?", "m.seq > ?"}
	args := []any{boardID, q.After}
	if q.Before > 0 {
		where, args = append(where, "m.seq < ?"), append(args, q.Before)
	}
	if !readAll {
		where, args = append(where, "(m.sender_id = ? OR "+addressedTo+")"), append(args, reader.ID, name, role)
	}
	if q.FromID != "" {
		where, args = append(where, "m.sender_id = ?"), append(args, q.FromID)
	}
	if q.SenderRole != "" {
		where, args = append(where, "s.role = ?"), append(args, q.SenderRole)
	}
	if q.ToMe {
		where, args = append(where, "m.sender_id <> ? AND "+addressedTo), append(args, reader.ID, name, role)
	}
	order := "m.seq"
	if q.Newest {
		order = "m.seq DESC"
	}
	ms, err := t.queryMessages(strings.Join(where, " AND ")+" ORDER BY "+order+" LIMIT ?", append(args, q.Limit)...)
	if q.Newest {
		slices.Reverse(ms)
	}
	return ms, err
}

// Inbox returns messages after the reader's cursor that are addressed to it and that it
// didn't send, oldest first.
func (t *tx) Inbox(reader board.Member, limit int) ([]board.Message, error) {
	name, role := targetsOf(reader)
	return t.queryMessages("m.board_id = ? AND m.seq > ? AND m.sender_id <> ? AND "+addressedTo+" ORDER BY m.seq LIMIT ?",
		reader.BoardID, reader.Cursor, reader.ID, name, role, limit)
}

// MessageByID finds a message.
func (t *tx) MessageByID(id string) (board.Message, error) {
	ms, err := t.queryMessages("m.id = ?", id)
	if err != nil {
		return board.Message{}, err
	}
	if len(ms) == 0 {
		return board.Message{}, board.ErrNotFound
	}
	return ms[0], nil
}

// MessagesBySeq returns the messages among seqs, keyed by seq.
func (t *tx) MessagesBySeq(boardID string, seqs []int64) (map[int64]board.Message, error) {
	out := map[int64]board.Message{}
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
