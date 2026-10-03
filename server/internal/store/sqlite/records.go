package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// InsertHuman adds a human.
func (t *tx) InsertHuman(h board.Human) error {
	return t.exec("INSERT INTO humans (id, name, token_digest, created_at) VALUES (?, ?, ?, ?)",
		h.ID, h.Name, h.TokenDigest, h.CreatedAt)
}

// HumanByTokenDigest finds the human whose token has this digest.
func (t *tx) HumanByTokenDigest(digest string) (board.Human, error) {
	var h board.Human
	err := t.queryRow("SELECT id, name, token_digest, created_at FROM humans WHERE token_digest = ?", digest).
		Scan(&h.ID, &h.Name, &h.TokenDigest, &h.CreatedAt)
	return h, notFound(err)
}

// HumanCount returns how many humans exist.
func (t *tx) HumanCount() (int, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM humans").Scan(&n)
	return n, err
}

const boardColumns = "id, name, title, template, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by"

func scanBoard(row interface{ Scan(...any) error }) (board.Board, error) {
	var b board.Board
	var roles, policy string
	if err := row.Scan(&b.ID, &b.Name, &b.Title, &b.Template, &b.Charter, &roles, &policy, &b.HeadSeq, &b.HeadHash, &b.CreatedAt, &b.CreatedBy); err != nil {
		return board.Board{}, notFound(err)
	}
	if err := json.Unmarshal([]byte(roles), &b.Roles); err != nil {
		return board.Board{}, fmt.Errorf("board %s roles: %w", b.ID, err)
	}
	if err := json.Unmarshal([]byte(policy), &b.Policy); err != nil {
		return board.Board{}, fmt.Errorf("board %s policy: %w", b.ID, err)
	}
	return b, nil
}

// InsertBoard adds a board.
func (t *tx) InsertBoard(b board.Board) error {
	roles, err := json.Marshal(b.Roles)
	if err != nil {
		return fmt.Errorf("encode roles: %w", err)
	}
	policy, err := json.Marshal(b.Policy)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return t.exec("INSERT INTO boards ("+boardColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.Title, b.Template, b.Charter, string(roles), string(policy), b.HeadSeq, b.HeadHash, b.CreatedAt, b.CreatedBy)
}

// SetBoardPolicy replaces a board's policy.
func (t *tx) SetBoardPolicy(boardID string, p rules.Policy) error {
	policy, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return t.exec("UPDATE boards SET policy_json = ? WHERE id = ?", string(policy), boardID)
}

// SetBoardTitle replaces a board's title; nil removes it.
func (t *tx) SetBoardTitle(boardID string, title *string) error {
	return t.exec("UPDATE boards SET title = ? WHERE id = ?", title, boardID)
}

// BoardByName finds a board by name.
func (t *tx) BoardByName(name string) (board.Board, error) {
	return scanBoard(t.queryRow("SELECT "+boardColumns+" FROM boards WHERE name = ?", name))
}

// BoardByID finds a board by id.
func (t *tx) BoardByID(id string) (board.Board, error) {
	return scanBoard(t.queryRow("SELECT "+boardColumns+" FROM boards WHERE id = ?", id))
}

// BoardNameTaken reports whether a board already has this name.
func (t *tx) BoardNameTaken(name string) (bool, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM boards WHERE name = ?", name).Scan(&n)
	return n > 0, err
}

// BoardsOfHuman lists the boards a human is a member of, by name.
func (t *tx) BoardsOfHuman(humanID string) ([]board.Board, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+boardColumns+" FROM boards WHERE id IN "+
		"(SELECT board_id FROM members WHERE human_id = ? AND kind = 'human') ORDER BY name", humanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

const (
	memberInsertColumns = "id, board_id, name, kind, role, human_id, owner, harness, token_digest, access, status, cursor, joined_at"
	memberColumns       = memberInsertColumns + ", presence, presence_since, presence_at"
)

func scanMember(row interface{ Scan(...any) error }) (board.Member, error) {
	var m board.Member
	var access, presence, since, at sql.NullString
	err := row.Scan(&m.ID, &m.BoardID, &m.Name, &m.Kind, &m.Role, &m.HumanID, &m.Owner, &m.Harness, &m.TokenDigest, &access, &m.Status, &m.Cursor, &m.JoinedAt,
		&presence, &since, &at)
	m.Access = access.String
	m.Presence = board.Presence{State: presence.String, Since: since.String, At: at.String}
	return m, notFound(err)
}

// SetPresence records an agent's presence, when it began and when it was reported.
func (t *tx) SetPresence(memberID string, p board.Presence) error {
	return t.exec("UPDATE members SET presence = ?, presence_since = ?, presence_at = ? WHERE id = ?", p.State, p.Since, p.At, memberID)
}

// InsertMember adds a member.
func (t *tx) InsertMember(m board.Member) error {
	return t.exec("INSERT INTO members ("+memberInsertColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)",
		m.ID, m.BoardID, m.Name, m.Kind, m.Role, m.HumanID, m.Owner, m.Harness, m.TokenDigest, m.Access, m.Status, m.Cursor, m.JoinedAt)
}

// MemberByTokenDigest finds the agent whose token has this digest.
func (t *tx) MemberByTokenDigest(digest string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE token_digest = ?", digest))
}

// HumanMember finds a human's own membership of a board.
func (t *tx) HumanMember(boardID, humanID string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE board_id = ? AND human_id = ? AND kind = 'human'", boardID, humanID))
}

// MemberByName finds a member of a board by name.
func (t *tx) MemberByName(boardID, name string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE board_id = ? AND name = ?", boardID, name))
}

// Members lists a board's members in the order they joined.
func (t *tx) Members(boardID string) ([]board.Member, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+memberColumns+" FROM members WHERE board_id = ? ORDER BY rowid", boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetCursor moves a member's read position forward; it never moves it back.
func (t *tx) SetCursor(memberID string, seq int64) error {
	return t.exec("UPDATE members SET cursor = max(cursor, ?) WHERE id = ?", seq, memberID)
}

const joinCodeColumns = "id, board_id, code_digest, role, expires_at, created_at, created_by, revoked_at"

func scanJoinCode(row *sql.Row) (board.JoinCode, error) {
	var j board.JoinCode
	err := row.Scan(&j.ID, &j.BoardID, &j.CodeDigest, &j.Role, &j.ExpiresAt, &j.CreatedAt, &j.CreatedBy, &j.RevokedAt)
	return j, notFound(err)
}

// InsertJoinCode adds a join code.
func (t *tx) InsertJoinCode(j board.JoinCode) error {
	return t.exec("INSERT INTO join_codes ("+joinCodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		j.ID, j.BoardID, j.CodeDigest, j.Role, j.ExpiresAt, j.CreatedAt, j.CreatedBy, j.RevokedAt)
}

// JoinCodeByDigest finds a join code by the digest of the code.
func (t *tx) JoinCodeByDigest(digest string) (board.JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE code_digest = ?", digest))
}

// JoinCodeByID finds a join code by id.
func (t *tx) JoinCodeByID(id string) (board.JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE id = ?", id))
}

// RevokeJoinCode marks a join code revoked.
func (t *tx) RevokeJoinCode(id, at string) error {
	return t.exec("UPDATE join_codes SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", at, id)
}

// AppendEvent adds the next event to its board's log and moves the board's head. The
// event's seq must be exactly one past the current head. The head moves first, so an
// event that would leave a gap is refused before anything is written.
func (t *tx) AppendEvent(e events.Event) error {
	actor, err := json.Marshal(e.Actor)
	if err != nil {
		return fmt.Errorf("encode actor: %w", err)
	}
	res, err := t.tx.ExecContext(t.ctx, "UPDATE boards SET head_seq = ?, head_hash = ? WHERE id = ? AND head_seq = ?", e.Seq, e.Hash, e.BoardID, e.Seq-1)
	if err != nil {
		return fmt.Errorf("move board head: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("move board head to %d: seq is not one past the head", e.Seq)
	}
	if err := t.exec("INSERT INTO events (board_id, seq, id, type, at, actor_json, data_json, data_hash, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		e.BoardID, e.Seq, e.ID, e.Type, e.At, string(actor), string(e.Data), e.DataHash, e.PrevHash, e.Hash); err != nil {
		return fmt.Errorf("append event %d: %w", e.Seq, err)
	}
	return nil
}

// Events returns a board's events after seq, oldest first.
func (t *tx) Events(boardID string, after int64, limit int) ([]events.Event, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id, board_id, seq, type, at, actor_json, data_json, data_hash, prev_hash, hash FROM events WHERE board_id = ? AND seq > ? ORDER BY seq LIMIT ?", boardID, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []events.Event
	for rows.Next() {
		var e events.Event
		var actor, data string
		if err := rows.Scan(&e.ID, &e.BoardID, &e.Seq, &e.Type, &e.At, &actor, &data, &e.DataHash, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(actor), &e.Actor); err != nil {
			return nil, fmt.Errorf("event %d actor: %w", e.Seq, err)
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
