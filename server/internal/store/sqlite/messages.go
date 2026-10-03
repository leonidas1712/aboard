package sqlite

import (
	"database/sql"
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
	if err := t.exec("INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, thread_root, urgent, expects_reply, redactions_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Seq, m.At, m.SenderID, string(to), m.Body, m.ReplyTo, m.ThreadRoot, m.Urgent, m.ExpectsReply, string(red)); err != nil {
		return err
	}
	return t.exec("UPDATE boards SET message_count = message_count + 1, last_message_at = ? WHERE id = ?", m.At, m.BoardID)
}

const messageSelect = `SELECT m.id, m.board_id, m.seq, m.at, m.sender_id, m.to_json, m.body, m.reply_to,
	m.urgent, m.expects_reply, m.redactions_json, s.name, s.kind, s.role, s.owner, s.human_id, s.harness, r.seq,
	m.thread_root, tr.seq,
	(SELECT COUNT(DISTINCT o.human_id) FROM members o WHERE o.board_id = m.board_id AND o.kind = 'agent')
	FROM messages m JOIN members s ON s.id = m.sender_id LEFT JOIN messages r ON r.id = m.reply_to
	LEFT JOIN messages tr ON tr.id = m.thread_root`

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
			&m.Urgent, &m.ExpectsReply, &red, &m.SenderName, &m.SenderKind, &m.SenderRole, &m.SenderOwner, &m.SenderHuman, &m.SenderHarness, &m.ReplyToSeq,
			&m.ThreadRoot, &m.ThreadRootSeq, &m.AgentOwners); err != nil {
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

// visibleTo returns the condition and arguments that keep the messages reader may see.
func visibleTo(reader board.Member, readAll bool) (cond string, args []any) {
	if readAll {
		return "1", nil
	}
	name, role := targetsOf(reader)
	return "(m.sender_id = ? OR " + addressedTo + ")", []any{reader.ID, name, role}
}

// countReplies counts the replies matching where in each thread, with the newest time.
func (t *tx) countReplies(where string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(t.ctx, "SELECT m.thread_root, COUNT(*), MAX(m.at) FROM messages m WHERE "+where+" GROUP BY m.thread_root", args...)
}

// Thread returns the replies in a thread that reader may see, oldest first.
func (t *tx) Thread(rootID string, reader board.Member, readAll bool, after int64, limit int) ([]board.Message, error) {
	visible, args := visibleTo(reader, readAll)
	return t.queryMessages("m.thread_root = ? AND m.seq > ? AND "+visible+" ORDER BY m.seq LIMIT ?",
		append(append([]any{rootID, after}, args...), limit)...)
}

// ThreadCounts counts the replies reader may see in each of the threads rootIDs start.
func (t *tx) ThreadCounts(reader board.Member, readAll bool, rootIDs []string) (map[string]board.ThreadCount, error) {
	out := map[string]board.ThreadCount{}
	if len(rootIDs) == 0 {
		return out, nil
	}
	visible, vargs := visibleTo(reader, readAll)
	args := make([]any, 0, len(rootIDs)+len(vargs))
	for _, id := range rootIDs {
		args = append(args, id)
	}
	rows, err := t.countReplies("m.thread_root IN (?"+strings.Repeat(", ?", len(rootIDs)-1)+") AND "+visible, append(args, vargs...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	for rows.Next() {
		var root string
		var c board.ThreadCount
		if err := rows.Scan(&root, &c.Replies, &c.LastAt); err != nil {
			return nil, err
		}
		out[root] = c
	}
	return out, rows.Err()
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
