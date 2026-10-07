package sqlite

import (
	"strings"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (t *tx) TaskMessageCounts(boardID, taskID string, reader board.Member, readAll bool, since int64) (messages, threads, messagesSince int, err error) {
	where := "m.board_id = ? AND EXISTS (SELECT 1 FROM json_each(m.about_json) WHERE json_extract(value, '$.id') = ?)"
	args := []any{boardID, taskID}
	if !readAll {
		name, role := targetsOf(reader)
		where += " AND (m.sender_id = ? OR " + addressedTo + ")"
		args = append(args, reader.ID, name, role)
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT m.seq, COALESCE(m.thread_root, m.id) FROM messages m WHERE "+where, args...)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var seq int64
		var thread string
		if err := rows.Scan(&seq, &thread); err != nil {
			return 0, 0, 0, err
		}
		messages++
		seen[thread] = true
		if since > 0 && seq > since {
			messagesSince++
		}
	}
	return messages, len(seen), messagesSince, rows.Err()
}

func (t *tx) PostsWithoutTask(memberID string) (int, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT about_json FROM messages WHERE sender_id = ? ORDER BY seq DESC", memberID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var about string
		if err := rows.Scan(&about); err != nil {
			return 0, err
		}
		if strings.TrimSpace(about) != "[]" {
			break
		}
		n++
	}
	return n, rows.Err()
}
