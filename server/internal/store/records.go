package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Human is a person with a login on this server.
type Human struct {
	ID          string
	Name        string
	TokenDigest string
	CreatedAt   string
}

// InsertHuman adds a human.
func (t *Tx) InsertHuman(h Human) error {
	return t.exec("INSERT INTO humans (id, name, token_digest, created_at) VALUES (?, ?, ?, ?)",
		h.ID, h.Name, h.TokenDigest, h.CreatedAt)
}

// HumanByTokenDigest finds the human whose token has this digest.
func (t *Tx) HumanByTokenDigest(digest string) (Human, error) {
	var h Human
	err := t.queryRow("SELECT id, name, token_digest, created_at FROM humans WHERE token_digest = ?", digest).
		Scan(&h.ID, &h.Name, &h.TokenDigest, &h.CreatedAt)
	return h, notFound(err)
}

// HumanCount returns how many humans exist.
func (t *Tx) HumanCount() (int, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM humans").Scan(&n)
	return n, err
}

// Board is a board's current state.
type Board struct {
	ID        string
	Name      string
	Template  *string
	Charter   string
	Roles     map[string]rules.Role
	Policy    rules.Policy
	HeadSeq   int64
	HeadHash  string
	CreatedAt string
	CreatedBy string // member id of the creating human
}

const boardColumns = "id, name, template, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by"

func scanBoard(row interface{ Scan(...any) error }) (Board, error) {
	var b Board
	var roles, policy string
	if err := row.Scan(&b.ID, &b.Name, &b.Template, &b.Charter, &roles, &policy, &b.HeadSeq, &b.HeadHash, &b.CreatedAt, &b.CreatedBy); err != nil {
		return Board{}, notFound(err)
	}
	if err := json.Unmarshal([]byte(roles), &b.Roles); err != nil {
		return Board{}, fmt.Errorf("board %s roles: %w", b.ID, err)
	}
	if err := json.Unmarshal([]byte(policy), &b.Policy); err != nil {
		return Board{}, fmt.Errorf("board %s policy: %w", b.ID, err)
	}
	return b, nil
}

// InsertBoard adds a board.
func (t *Tx) InsertBoard(b Board) error {
	roles, err := json.Marshal(b.Roles)
	if err != nil {
		return fmt.Errorf("encode roles: %w", err)
	}
	policy, err := json.Marshal(b.Policy)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return t.exec("INSERT INTO boards ("+boardColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.Template, b.Charter, string(roles), string(policy), b.HeadSeq, b.HeadHash, b.CreatedAt, b.CreatedBy)
}

// SetBoardPolicy replaces a board's policy.
func (t *Tx) SetBoardPolicy(boardID string, p rules.Policy) error {
	policy, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode policy: %w", err)
	}
	return t.exec("UPDATE boards SET policy_json = ? WHERE id = ?", string(policy), boardID)
}

// BoardByName finds a board by name.
func (t *Tx) BoardByName(name string) (Board, error) {
	return scanBoard(t.queryRow("SELECT "+boardColumns+" FROM boards WHERE name = ?", name))
}

// BoardByID finds a board by id.
func (t *Tx) BoardByID(id string) (Board, error) {
	return scanBoard(t.queryRow("SELECT "+boardColumns+" FROM boards WHERE id = ?", id))
}

// BoardNameTaken reports whether a board already has this name.
func (t *Tx) BoardNameTaken(name string) (bool, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM boards WHERE name = ?", name).Scan(&n)
	return n > 0, err
}

// BoardsOfHuman lists the boards a human is a member of, by name.
func (t *Tx) BoardsOfHuman(humanID string) ([]Board, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+boardColumns+" FROM boards WHERE id IN "+
		"(SELECT board_id FROM members WHERE human_id = ? AND kind = 'human') ORDER BY name", humanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Member is a human or agent on a board.
type Member struct {
	ID          string
	BoardID     string
	Name        string
	Kind        string
	Role        *string
	HumanID     string // the human, or the agent's owner
	Owner       *string
	Harness     *string
	TokenDigest *string
	Status      string
	Cursor      int64
	JoinedAt    string
}

// Rules returns what the rules package needs to know about the member.
func (m Member) Rules() rules.Member {
	r := rules.Member{ID: m.ID, Name: m.Name, Kind: m.Kind}
	if m.Role != nil {
		r.Role = *m.Role
	}
	return r
}

const memberColumns = "id, board_id, name, kind, role, human_id, owner, harness, token_digest, status, cursor, joined_at"

func scanMember(row interface{ Scan(...any) error }) (Member, error) {
	var m Member
	err := row.Scan(&m.ID, &m.BoardID, &m.Name, &m.Kind, &m.Role, &m.HumanID, &m.Owner, &m.Harness, &m.TokenDigest, &m.Status, &m.Cursor, &m.JoinedAt)
	return m, notFound(err)
}

// InsertMember adds a member.
func (t *Tx) InsertMember(m Member) error {
	return t.exec("INSERT INTO members ("+memberColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Name, m.Kind, m.Role, m.HumanID, m.Owner, m.Harness, m.TokenDigest, m.Status, m.Cursor, m.JoinedAt)
}

// MemberByTokenDigest finds the agent whose token has this digest.
func (t *Tx) MemberByTokenDigest(digest string) (Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE token_digest = ?", digest))
}

// HumanMember finds a human's own membership of a board.
func (t *Tx) HumanMember(boardID, humanID string) (Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE board_id = ? AND human_id = ? AND kind = 'human'", boardID, humanID))
}

// MemberByName finds a member of a board by name.
func (t *Tx) MemberByName(boardID, name string) (Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE board_id = ? AND name = ?", boardID, name))
}

// Members lists a board's members in the order they joined.
func (t *Tx) Members(boardID string) ([]Member, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+memberColumns+" FROM members WHERE board_id = ? ORDER BY rowid", boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []Member
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
func (t *Tx) SetCursor(memberID string, seq int64) error {
	return t.exec("UPDATE members SET cursor = max(cursor, ?) WHERE id = ?", seq, memberID)
}

// JoinCode is a code that lets sessions join a board in one role.
type JoinCode struct {
	ID         string
	BoardID    string
	CodeDigest string
	Role       string
	ExpiresAt  string
	CreatedAt  string
	CreatedBy  string
	RevokedAt  *string
}

const joinCodeColumns = "id, board_id, code_digest, role, expires_at, created_at, created_by, revoked_at"

func scanJoinCode(row *sql.Row) (JoinCode, error) {
	var j JoinCode
	err := row.Scan(&j.ID, &j.BoardID, &j.CodeDigest, &j.Role, &j.ExpiresAt, &j.CreatedAt, &j.CreatedBy, &j.RevokedAt)
	return j, notFound(err)
}

// InsertJoinCode adds a join code.
func (t *Tx) InsertJoinCode(j JoinCode) error {
	return t.exec("INSERT INTO join_codes ("+joinCodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		j.ID, j.BoardID, j.CodeDigest, j.Role, j.ExpiresAt, j.CreatedAt, j.CreatedBy, j.RevokedAt)
}

// JoinCodeByDigest finds a join code by the digest of the code.
func (t *Tx) JoinCodeByDigest(digest string) (JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE code_digest = ?", digest))
}

// JoinCodeByID finds a join code by id.
func (t *Tx) JoinCodeByID(id string) (JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE id = ?", id))
}

// RevokeJoinCode marks a join code revoked.
func (t *Tx) RevokeJoinCode(id, at string) error {
	return t.exec("UPDATE join_codes SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", at, id)
}

// AppendEvent adds the next event to its board's log and moves the board's head. The
// event's seq must be exactly one past the current head.
func (t *Tx) AppendEvent(e events.Event) error {
	actor, err := json.Marshal(e.Actor)
	if err != nil {
		return fmt.Errorf("encode actor: %w", err)
	}
	if err := t.exec("INSERT INTO events (board_id, seq, id, type, at, actor_json, data_json, data_hash, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		e.BoardID, e.Seq, e.ID, e.Type, e.At, string(actor), string(e.Data), e.DataHash, e.PrevHash, e.Hash); err != nil {
		return fmt.Errorf("append event %d: %w", e.Seq, err)
	}
	res, err := t.tx.ExecContext(t.ctx, "UPDATE boards SET head_seq = ?, head_hash = ? WHERE id = ? AND head_seq = ?", e.Seq, e.Hash, e.BoardID, e.Seq-1)
	if err != nil {
		return fmt.Errorf("move board head: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("move board head to %d: board head moved concurrently", e.Seq)
	}
	return nil
}

// Events returns a board's events after seq, oldest first.
func (t *Tx) Events(boardID string, after int64, limit int) ([]events.Event, error) {
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
