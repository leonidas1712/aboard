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
	var recipients *string
	if m.Recipients != nil {
		b, err := json.Marshal(m.Recipients)
		if err != nil {
			return fmt.Errorf("encode recipients: %w", err)
		}
		s := string(b)
		recipients = &s
	}
	if m.Mentions == nil {
		m.Mentions = []board.Mention{}
	}
	men, err := json.Marshal(m.Mentions)
	if err != nil {
		return fmt.Errorf("encode mentions: %w", err)
	}
	if m.About == nil {
		m.About = []board.TaskTag{}
	}
	about, err := json.Marshal(m.About)
	if err != nil {
		return fmt.Errorf("encode task tags: %w", err)
	}
	ask, err := json.Marshal(m.Ask)
	if err != nil {
		return err
	}
	answer, err := json.Marshal(m.Answer)
	if err != nil {
		return err
	}
	if err := t.exec("INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, thread_root, urgent, expects_reply, redactions_json, recipients_json, mentions_json, about_json, ask_json, answer_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Seq, m.At, m.SenderID, string(to), m.Body, m.ReplyTo, m.ThreadRoot, m.Urgent, m.ExpectsReply, string(red), recipients, string(men), string(about), string(ask), string(answer)); err != nil {
		return err
	}
	return t.exec("UPDATE boards SET message_count = message_count + 1, last_message_at = ? WHERE id = ?", m.At, m.BoardID)
}

const messageSelect = `SELECT m.id, m.board_id, m.seq, m.at, m.sender_id, m.to_json, m.body, m.reply_to,
	m.urgent, m.expects_reply, m.redactions_json, m.mentions_json, s.name, s.kind, s.role, s.owner, s.human_id, s.harness, r.seq,
	m.thread_root, tr.seq, rs.name, m.recipients_json,
	(SELECT COUNT(DISTINCT o.human_id) FROM members o WHERE o.board_id = m.board_id AND o.kind = 'agent'), m.about_json, m.ask_json, m.answer_json
	FROM messages m JOIN members s ON s.id = m.sender_id LEFT JOIN messages r ON r.id = m.reply_to
	LEFT JOIN members rs ON rs.id = r.sender_id LEFT JOIN messages tr ON tr.id = m.thread_root`

// addressedTo is a SQL condition matching messages whose targets include all, @name or
// role:R. It takes the @name and role:R strings as parameters.
var addressedTo = addressedToAs("m")

// addressedToAs is addressedTo for the messages table under another alias.
func addressedToAs(alias string) string {
	return `EXISTS (SELECT 1 FROM json_each(` + alias + `.to_json) WHERE value IN ('all', ?, ?))`
}

func (t *tx) queryMessages(where string, args ...any) ([]board.Message, error) {
	rows, err := t.tx.QueryContext(t.ctx, messageSelect+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.Message
	for rows.Next() {
		var m board.Message
		var to, red, men, about, ask, answer string
		var recipients *string
		if err := rows.Scan(&m.ID, &m.BoardID, &m.Seq, &m.At, &m.SenderID, &to, &m.Body, &m.ReplyTo,
			&m.Urgent, &m.ExpectsReply, &red, &men, &m.SenderName, &m.SenderKind, &m.SenderRole, &m.SenderOwner, &m.SenderHuman, &m.SenderHarness, &m.ReplyToSeq,
			&m.ThreadRoot, &m.ThreadRootSeq, &m.ReplyToFrom, &recipients, &m.AgentOwners, &about, &ask, &answer); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ask), &m.Ask); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(answer), &m.Answer); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(men), &m.Mentions); err != nil {
			return nil, fmt.Errorf("decode mentions: %w", err)
		}
		if err := json.Unmarshal([]byte(about), &m.About); err != nil {
			return nil, fmt.Errorf("decode task tags: %w", err)
		}
		if recipients != nil {
			if err := json.Unmarshal([]byte(*recipients), &m.Recipients); err != nil {
				return nil, fmt.Errorf("message %s recipients: %w", m.ID, err)
			}
			if m.Recipients == nil {
				m.Recipients = []string{}
			}
		}
		if err := json.Unmarshal([]byte(to), &m.To); err != nil {
			return nil, fmt.Errorf("message %s targets: %w", m.ID, err)
		}
		if err := json.Unmarshal([]byte(red), &m.Redactions); err != nil {
			return nil, fmt.Errorf("message %s redactions: %w", m.ID, err)
		}
		if err := json.Unmarshal([]byte(men), &m.Mentions); err != nil {
			return nil, fmt.Errorf("message %s mentions: %w", m.ID, err)
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
	if q.TaskID != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(m.about_json) WHERE json_extract(value, '$.id') = ?)")
		args = append(args, q.TaskID)
	}
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

// Inbox returns messages after the reader's cursor that it didn't send and that are
// addressed to it or, when mentions is true, mention it with wakes set, oldest first.
func (t *tx) Inbox(reader board.Member, mentions bool, limit int) ([]board.Message, error) {
	name, role := targetsOf(reader)
	return t.queryMessages("m.board_id = ? AND m.seq > ? AND m.sender_id <> ? AND ("+addressedTo+
		" OR (? AND EXISTS (SELECT 1 FROM json_each(m.mentions_json) WHERE json_extract(value, '$.id') = ? AND json_extract(value, '$.wakes'))))"+
		" ORDER BY m.seq LIMIT ?",
		reader.BoardID, reader.Cursor, reader.ID, name, role, mentions, reader.ID, limit)
}

// CountUnread counts the same unread messages as Inbox for an agent, or every message
// after a person's cursor that they didn't send.
func (t *tx) CountUnread(reader board.Member, addressedOnly, mentions bool) (int64, error) {
	where, args := "m.board_id = ? AND m.seq > ? AND m.sender_id <> ?", []any{reader.BoardID, reader.Cursor, reader.ID}
	if addressedOnly {
		name, role := targetsOf(reader)
		where += " AND (" + addressedTo + " OR (? AND EXISTS (SELECT 1 FROM json_each(m.mentions_json) WHERE json_extract(value, '$.id') = ? AND json_extract(value, '$.wakes'))))"
		args = append(args, name, role, mentions, reader.ID)
	}
	var n int64
	err := t.tx.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM messages m WHERE "+where, args...).Scan(&n)
	return n, err
}

// CountNeedsReply uses recorded recipient ids so a reused name cannot inherit questions.
func (t *tx) CountNeedsReply(reader board.Member) (int64, error) {
	var n int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM messages m
		WHERE m.board_id = ? AND m.expects_reply AND m.ask_json = 'null' AND m.sender_id <> ?
		AND EXISTS (SELECT 1 FROM json_each(m.recipients_json) WHERE value = ?)
		AND NOT EXISTS (SELECT 1 FROM messages r WHERE r.reply_to = m.id AND r.sender_id = ?)`,
		reader.BoardID, reader.ID, reader.ID, reader.ID).Scan(&n)
	return n, err
}

// visibleTo returns the condition and arguments that keep the messages reader may see.
func visibleTo(reader board.Member, readAll bool) (cond string, args []any) {
	return visibleAs("m", reader, readAll)
}

// visibleAs is visibleTo for the messages table under another alias.
func visibleAs(alias string, reader board.Member, readAll bool) (cond string, args []any) {
	if readAll {
		return "1", nil
	}
	name, role := targetsOf(reader)
	return "(" + alias + ".sender_id = ? OR " + addressedToAs(alias) + ")", []any{reader.ID, name, role}
}

// in returns "(?, ?, …)" for n parameters, and the values as arguments.
func in(values []string) (list string, args []any) {
	args = make([]any, 0, len(values))
	for _, v := range values {
		args = append(args, v)
	}
	return "(?" + strings.Repeat(", ?", len(values)-1) + ")", args
}

// queryWhere runs from + " WHERE " + where + " " + rest. Every value is a parameter in
// args; the conditions only hold placeholders.
func (t *tx) queryWhere(from, where, rest string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(t.ctx, from+" WHERE "+where+" "+rest, args...)
}

// Threads lists the board's threads with a reply reader may see whose first message it
// may see too, the newest such reply first.
func (t *tx) Threads(boardID string, reader board.Member, readAll bool, limit int) ([]board.ThreadInfo, error) {
	reply, rargs := visibleAs("m", reader, readAll)
	root, oargs := visibleAs("r", reader, readAll)
	rows, err := t.queryWhere("SELECT m.thread_root FROM messages m JOIN messages r ON r.id = m.thread_root",
		"m.board_id = ? AND "+reply+" AND "+root, "GROUP BY m.thread_root ORDER BY MAX(m.seq) DESC LIMIT ?",
		append(append(append([]any{boardID}, rargs...), oargs...), limit)...)
	if err != nil {
		return nil, err
	}
	var out []board.ThreadInfo
	for rows.Next() {
		var info board.ThreadInfo
		if err := rows.Scan(&info.RootID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, info)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}

	// Who replied in each thread, in the order they first replied.
	ids := make([]string, 0, len(out))
	for _, info := range out {
		ids = append(ids, info.RootID)
	}
	list, args := in(ids)
	rows, err = t.queryWhere("SELECT m.thread_root, s.name FROM messages m JOIN members s ON s.id = m.sender_id",
		"m.thread_root IN "+list+" AND "+reply, "GROUP BY m.thread_root, m.sender_id ORDER BY MIN(m.seq)",
		append(args, rargs...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	repliers := map[string][]string{}
	for rows.Next() {
		var rootID, name string
		if err := rows.Scan(&rootID, &name); err != nil {
			return nil, err
		}
		repliers[rootID] = append(repliers[rootID], name)
	}
	for i := range out {
		out[i].Repliers = repliers[out[i].RootID]
	}
	return out, rows.Err()
}

// InsertReaction adds a member's reaction to a message.
func (t *tx) InsertReaction(r board.Reaction) error {
	return t.exec("INSERT INTO reactions (message_id, member_id, name, at) VALUES (?, ?, ?, ?)", r.MessageID, r.MemberID, r.Name, r.At)
}

// DeleteReaction removes a member's reaction to a message, if there is one.
func (t *tx) DeleteReaction(messageID, memberID, name string) error {
	return t.exec("DELETE FROM reactions WHERE message_id = ? AND member_id = ? AND name = ?", messageID, memberID, name)
}

// Reactions returns the reactions to each message that has any, oldest first.
func (t *tx) Reactions(messageIDs []string) (map[string][]board.Reaction, error) {
	out := map[string][]board.Reaction{}
	if len(messageIDs) == 0 {
		return out, nil
	}
	list, args := in(messageIDs)
	rows, err := t.queryWhere("SELECT r.message_id, r.member_id, s.name, r.name, r.at FROM reactions r JOIN members s ON s.id = r.member_id",
		"r.message_id IN "+list, "ORDER BY r.at, r.rowid", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	for rows.Next() {
		var r board.Reaction
		if err := rows.Scan(&r.MessageID, &r.MemberID, &r.MemberName, &r.Name, &r.At); err != nil {
			return nil, err
		}
		out[r.MessageID] = append(out[r.MessageID], r)
	}
	return out, rows.Err()
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

// Asks returns ask messages by their original sequence.
func (t *tx) Asks(boardID string) ([]board.Message, error) {
	return t.queryMessages("m.board_id = ? AND m.ask_json <> 'null' ORDER BY m.seq", boardID)
}

// LatestAnswer reads the last recorded decision or withdrawal.
func (t *tx) LatestAnswer(askID string) (board.Message, error) {
	ms, e := t.queryMessages("json_extract(m.answer_json, '$.ask_id') = ? ORDER BY m.seq DESC LIMIT 1", askID)
	if e != nil {
		return board.Message{}, e
	}
	if len(ms) == 0 {
		return board.Message{}, board.ErrNotFound
	}
	return ms[0], nil
}
