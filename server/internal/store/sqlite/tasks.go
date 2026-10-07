package sqlite

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/board"
)

const taskColumns = "id, board_id, number, ref, title, state, about_json, stands_json, owner_id, helpers_json, opened_by, opened_at, updated_at, closed_at, closed_note"

type storedTaskText struct {
	Seq     int64  `json:"seq"`
	Text    string `json:"text"`
	By      string `json:"by"`
	At      string `json:"at"`
	Version int64  `json:"version"`
}

func (t *tx) scanTask(row interface{ Scan(...any) error }) (board.Task, error) {
	var out board.Task
	var about, stands, owner sql.NullString
	var helpers, opener string
	e := row.Scan(&out.ID, &out.BoardID, &out.Number, &out.Ref, &out.Title, &out.State, &about, &stands, &owner, &helpers, &opener, &out.OpenedAt, &out.UpdatedAt, &out.ClosedAt, &out.ClosedNote)
	if e != nil {
		return out, notFound(e)
	}
	b, e := t.BoardByID(out.BoardID)
	if e != nil {
		return out, e
	}
	out.Board = b.Name
	out.OpenedBy, e = t.MemberByID(opener)
	if e != nil {
		return out, e
	}
	if owner.Valid {
		m, e := t.MemberByID(owner.String)
		if e != nil {
			return out, e
		}
		out.Owner = &m
	}
	var ids []string
	if e := json.Unmarshal([]byte(helpers), &ids); e != nil {
		return out, e
	}
	out.Helpers = []board.Member{}
	for _, id := range ids {
		m, e := t.MemberByID(id)
		if e != nil {
			return out, e
		}
		out.Helpers = append(out.Helpers, m)
	}
	decode := func(raw sql.NullString) (*board.TaskText, error) {
		if !raw.Valid {
			return nil, nil
		}
		var v storedTaskText
		if e := json.Unmarshal([]byte(raw.String), &v); e != nil {
			return nil, e
		}
		m, e := t.MemberByID(v.By)
		if e != nil {
			return nil, e
		}
		return &board.TaskText{Text: v.Text, By: m, At: v.At, Version: v.Version, Seq: v.Seq}, nil
	}
	out.About, e = decode(about)
	if e != nil {
		return out, e
	}
	out.Stands, e = decode(stands)
	return out, e
}

// TaskBySelector resolves a permanent id, reference or board-local number.
func (t *tx) TaskBySelector(boardID, sel string) (board.Task, error) {
	if n, e := strconv.ParseInt(sel, 10, 64); e == nil && n > 0 {
		return t.scanTask(t.queryRow("SELECT "+taskColumns+" FROM tasks WHERE board_id=? AND number=?", boardID, n))
	}
	return t.scanTask(t.queryRow("SELECT "+taskColumns+" FROM tasks WHERE board_id=? AND (id=? OR ref=?)", boardID, sel, strings.ToUpper(sel)))
}

// Tasks lists a board's tasks in number order.
func (t *tx) Tasks(boardID string) ([]board.Task, error) {
	rows, e := t.tx.QueryContext(t.ctx, "SELECT id FROM tasks WHERE board_id=? ORDER BY number", boardID)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if e := rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	_ = rows.Close()
	if e != nil {
		return nil, e
	}
	out := []board.Task{}
	for _, id := range ids {
		x, e := t.TaskBySelector(boardID, id)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, nil
}

// TasksByID resolves ids only within their board.
func (t *tx) TasksByID(boardID string, ids []string) (map[string]board.Task, error) {
	out := map[string]board.Task{}
	for _, id := range ids {
		x, e := t.TaskBySelector(boardID, id)
		if e != nil {
			return nil, e
		}
		out[id] = x
	}
	return out, nil
}

// NextTaskNumber never reuses a task number.
func (t *tx) NextTaskNumber(boardID string) (int64, error) {
	var n int64
	e := t.queryRow("SELECT coalesce(max(number),0)+1 FROM tasks WHERE board_id=?", boardID).Scan(&n)
	return n, e
}

// TaskPrefixOwner includes prefixes no longer selected by their board.
func (t *tx) TaskPrefixOwner(prefix string) (string, error) {
	var id string
	e := t.queryRow("SELECT board_id FROM task_prefixes WHERE prefix=?", prefix).Scan(&id)
	return id, notFound(e)
}

// ReserveTaskPrefix retains this board's reservation forever.
func (t *tx) ReserveTaskPrefix(boardID, prefix string) error {
	return t.exec("INSERT INTO task_prefixes(prefix,board_id) VALUES(?,?) ON CONFLICT(prefix) DO NOTHING", prefix, boardID)
}

// SetTaskPrefix selects an already reserved prefix for new tasks.
func (t *tx) SetTaskPrefix(boardID, prefix string) error {
	return t.exec("UPDATE boards SET task_prefix=? WHERE id=?", prefix, boardID)
}

// SaveTask stores a read model after its event has been appended.
func (t *tx) SaveTask(x board.Task) error {
	encode := func(v *board.TaskText) (any, error) {
		if v == nil {
			return nil, nil
		}
		raw, e := json.Marshal(storedTaskText{Text: v.Text, By: v.By.ID, At: v.At, Version: v.Version, Seq: v.Seq})
		return string(raw), e
	}
	about, e := encode(x.About)
	if e != nil {
		return e
	}
	stands, e := encode(x.Stands)
	if e != nil {
		return e
	}
	var owner any
	if x.Owner != nil {
		owner = x.Owner.ID
	}
	helpers := []string{}
	for _, m := range x.Helpers {
		helpers = append(helpers, m.ID)
	}
	raw, e := json.Marshal(helpers)
	if e != nil {
		return e
	}
	return t.exec("INSERT INTO tasks ("+taskColumns+") VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,state=excluded.state,about_json=excluded.about_json,stands_json=excluded.stands_json,owner_id=excluded.owner_id,helpers_json=excluded.helpers_json,updated_at=excluded.updated_at,closed_at=excluded.closed_at,closed_note=excluded.closed_note", x.ID, x.BoardID, x.Number, x.Ref, x.Title, x.State, about, stands, owner, string(raw), x.OpenedBy.ID, x.OpenedAt, x.UpdatedAt, x.ClosedAt, x.ClosedNote)
}

// SetCurrentTask updates only the agent's record-derived selection.
func (t *tx) SetCurrentTask(memberID string, ref *board.TaskRef) error {
	var id any
	if ref != nil {
		id = ref.ID
	}
	return t.exec("UPDATE members SET current_task_id=? WHERE id=? AND kind='agent'", id, memberID)
}

// ClearTaskCurrent clears selections of the closed task.
func (t *tx) ClearTaskCurrent(taskID string) error {
	return t.exec("UPDATE members SET current_task_id=NULL WHERE current_task_id=?", taskID)
}

// ClearMemberTask clears only the member's selection of this task.
func (t *tx) ClearMemberTask(memberID, taskID string) error {
	return t.exec("UPDATE members SET current_task_id=NULL WHERE id=? AND current_task_id=?", memberID, taskID)
}
