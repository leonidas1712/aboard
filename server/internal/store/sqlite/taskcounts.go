package sqlite

import (
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/events"
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

// PostsWithoutTask reconstructs selection at each post, so an explicit untagged post
// made while working does not become taskless chatter after the task ends. Only the
// relevant record fields are read, never message bodies.
func (t *tx) PostsWithoutTask(memberID string) (int, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT type,
 COALESCE(json_extract(data_json,'$.task_id'),''),
 COALESCE(json_array_length(data_json,'$.about'),0)
 FROM events WHERE board_id=(SELECT board_id FROM members WHERE id=?)
 AND seq <= COALESCE((SELECT max(seq) FROM messages WHERE sender_id=?),0) AND (
 (type=? AND json_extract(actor_json,'$.member_id')=?) OR
 (type IN (?,?,?) AND json_extract(data_json,'$.member_id')=?) OR type=?)
 ORDER BY seq DESC`, memberID, memberID, events.MessagePosted, memberID, events.TaskStarted, events.TaskJoined, events.TaskDropped, memberID, events.TaskDone)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	// Between two selections the current task is fixed until its done/drop event. Reading
	// backwards records how many pending posts came after each possible clearing event.
	pending, n := 0, 0
	ended := map[string]int{}
	stopPosts := false
	for rows.Next() {
		var typ, task string
		var about int
		if err := rows.Scan(&typ, &task, &about); err != nil {
			return 0, err
		}
		switch typ {
		case events.TaskStarted, events.TaskJoined:
			if pending > 0 {
				after, ok := ended[task]
				if !ok {
					return n, nil
				}
				n += after
				if after < pending {
					return n, nil
				}
			}
			pending = 0
			clear(ended)
			if stopPosts {
				return n, nil
			}
		case events.TaskDone, events.TaskDropped:
			if _, seen := ended[task]; !seen {
				ended[task] = pending
			}
		case events.MessagePosted:
			if !stopPosts {
				if about == 0 {
					pending++
				} else {
					if pending == 0 {
						return n, nil
					}
					stopPosts = true
				}
			}
		}
	}
	return n + pending, rows.Err()
}
