package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// InsertHuman adds a human.
func (t *tx) InsertHuman(h board.Human) error {
	if _, err := t.ReservedHandle(h.Name); err == nil {
		return board.HandleTaken(h.Name)
	} else if !errors.Is(err, board.ErrNotFound) {
		return err
	}
	return t.exec("INSERT INTO humans (id, name, display_name, role, created_at) VALUES (?, ?, ?, ?, ?)",
		h.ID, h.Name, h.DisplayName, h.Role, h.CreatedAt)
}

const humanColumns = "id, name, display_name, role, created_at, removed_at, removed_by"

func scanHuman(row interface{ Scan(...any) error }) (board.Human, error) {
	var h board.Human
	err := row.Scan(&h.ID, &h.Name, &h.DisplayName, &h.Role, &h.CreatedAt, &h.RemovedAt, &h.RemovedBy)
	return h, notFound(err)
}

// HumanByID finds a human by id, removed or not.
func (t *tx) HumanByID(id string) (board.Human, error) {
	return scanHuman(t.queryRow("SELECT "+humanColumns+" FROM humans WHERE id = ?", id))
}

// HumanByName finds a human still on the server by handle.
func (t *tx) HumanByName(name string) (board.Human, error) {
	return scanHuman(t.queryRow("SELECT "+humanColumns+" FROM humans WHERE name = ? AND removed_at IS NULL", name))
}

// PeopleOnServer lists the people still on the server, oldest first.
func (t *tx) PeopleOnServer() ([]board.Human, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+humanColumns+" FROM humans WHERE removed_at IS NULL ORDER BY created_at, rowid")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.Human
	for rows.Next() {
		h, err := scanHuman(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AdminCount returns how many admins are still on the server.
func (t *tx) AdminCount() (int, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM humans WHERE role = 'admin' AND removed_at IS NULL").Scan(&n)
	return n, err
}

// SetHumanRole sets a person's server role.
func (t *tx) SetHumanRole(id, role string) error {
	return t.exec("UPDATE humans SET role = ? WHERE id = ?", role, id)
}

// RemoveHuman marks a person removed from the server, keeping the first time.
func (t *tx) RemoveHuman(id, at, by string) error {
	return t.exec("UPDATE humans SET removed_at = ?, removed_by = ? WHERE id = ? AND removed_at IS NULL", at, by, id)
}

const accessKeyColumns = "id, human_id, name, digest, created_at, expires_at, revoked_at, last_used_at, idle_seconds"

func scanAccessKey(row interface{ Scan(...any) error }, extra ...any) (board.AccessKey, error) {
	var k board.AccessKey
	err := row.Scan(append([]any{&k.ID, &k.HumanID, &k.Name, &k.Digest, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt, &k.IdleSeconds}, extra...)...)
	return k, notFound(err)
}

// InsertAccessKey adds an access key.
func (t *tx) InsertAccessKey(k board.AccessKey) error {
	return t.exec("INSERT INTO access_keys ("+accessKeyColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		k.ID, k.HumanID, k.Name, k.Digest, k.CreatedAt, k.ExpiresAt, k.RevokedAt, k.LastUsedAt, k.IdleSeconds)
}

// KeysOf lists a person's access keys, oldest first, with what depends on each.
func (t *tx) KeysOf(humanID, now string) ([]board.KeyUsage, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT `+accessKeyColumns+`,
		(SELECT count(*) FROM browser_logins b WHERE b.key_id = access_keys.id AND b.expires_at > ?),
		(SELECT count(*) FROM members m WHERE m.key_id = access_keys.id AND m.kind = 'agent' AND m.status = 'active'),
		(SELECT count(*) FROM delegations d WHERE d.key_id = access_keys.id AND d.ended_at IS NULL)
		FROM access_keys WHERE human_id = ? ORDER BY created_at, id`, now, humanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []board.KeyUsage
	for rows.Next() {
		var u board.KeyUsage
		if u.AccessKey, err = scanAccessKey(rows, &u.BrowserSessions, &u.AgentSeats, &u.Delegations); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// RevokeAccessKey marks an access key revoked, keeping the first time it was.
func (t *tx) RevokeAccessKey(id, at string) error {
	return t.exec("UPDATE access_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", at, id)
}

// UseAccessKey records a use of an access key, and moves its expiry when expires is set.
func (t *tx) UseAccessKey(id, at string, expires *string) error {
	return t.exec("UPDATE access_keys SET last_used_at = ?, expires_at = coalesce(?, expires_at) WHERE id = ?", at, expires, id)
}

// AccessKeyByDigest finds an access key by the digest of its secret.
func (t *tx) AccessKeyByDigest(digest string) (board.AccessKey, error) {
	return scanAccessKey(t.queryRow("SELECT "+accessKeyColumns+" FROM access_keys WHERE digest = ?", digest))
}

// AccessKeyByID finds an access key by id.
func (t *tx) AccessKeyByID(id string) (board.AccessKey, error) {
	return scanAccessKey(t.queryRow("SELECT "+accessKeyColumns+" FROM access_keys WHERE id = ?", id))
}

// NameUnnamedKeys gives every access key with an empty name this name.
func (t *tx) NameUnnamedKeys(name string) error {
	return t.exec("UPDATE access_keys SET name = ? WHERE name = ''", name)
}

const serverInviteColumns = "id, digest, created_by, created_at, expires_at, used_at, used_by"

// InsertServerInvite adds a server invite.
func (t *tx) InsertServerInvite(i board.ServerInvite) error {
	return t.exec("INSERT INTO server_invites ("+serverInviteColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		i.ID, i.Digest, i.CreatedBy, i.CreatedAt, i.ExpiresAt, i.UsedAt, i.UsedBy)
}

// ServerInviteByDigest finds a server invite by the digest of its secret.
func (t *tx) ServerInviteByDigest(digest string) (board.ServerInvite, error) {
	var i board.ServerInvite
	err := t.queryRow("SELECT "+serverInviteColumns+" FROM server_invites WHERE digest = ?", digest).
		Scan(&i.ID, &i.Digest, &i.CreatedBy, &i.CreatedAt, &i.ExpiresAt, &i.UsedAt, &i.UsedBy)
	return i, notFound(err)
}

// UseServerInvite marks an unused invite used, and reports whether it was unused.
func (t *tx) UseServerInvite(id, at, humanID string) (bool, error) {
	res, err := t.tx.ExecContext(t.ctx, "UPDATE server_invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL", at, humanID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// InsertBrowserLogin adds a browser login.
func (t *tx) InsertBrowserLogin(l board.BrowserLogin) error {
	return t.exec("INSERT INTO browser_logins ("+browserLoginColumns+") VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?)",
		l.ID, l.TokenDigest, l.HumanID, l.KeyID, l.StartedWith, l.CreatedAt, l.ExpiresAt)
}

const browserLoginColumns = "id, token_digest, human_id, key_id, started_with, created_at, expires_at"

func scanBrowserLogin(row interface{ Scan(...any) error }) (board.BrowserLogin, error) {
	var l board.BrowserLogin
	var key sql.NullString
	err := row.Scan(&l.ID, &l.TokenDigest, &l.HumanID, &key, &l.StartedWith, &l.CreatedAt, &l.ExpiresAt)
	l.KeyID = key.String
	return l, notFound(err)
}

// BrowserLoginByDigest finds a browser login by the digest of its token.
func (t *tx) BrowserLoginByDigest(digest string) (board.BrowserLogin, error) {
	return scanBrowserLogin(t.queryRow("SELECT "+browserLoginColumns+" FROM browser_logins WHERE token_digest = ?", digest))
}

// BrowserLoginByID finds a browser login by id.
func (t *tx) BrowserLoginByID(id string) (board.BrowserLogin, error) {
	return scanBrowserLogin(t.queryRow("SELECT "+browserLoginColumns+" FROM browser_logins WHERE id = ?", id))
}

// BrowserLoginsOf lists a human's browser logins that haven't expired at now, newest
// first.
func (t *tx) BrowserLoginsOf(humanID, now string) ([]board.BrowserLogin, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+browserLoginColumns+
		" FROM browser_logins WHERE human_id = ? AND expires_at > ? ORDER BY created_at DESC, id DESC", humanID, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []board.BrowserLogin
	for rows.Next() {
		l, err := scanBrowserLogin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteBrowserLogin removes one browser login by id.
func (t *tx) DeleteBrowserLogin(id string) error {
	return t.exec("DELETE FROM browser_logins WHERE id = ?", id)
}

// DeleteBrowserLogins removes every browser login of a human and returns how many had
// not expired at now.
func (t *tx) DeleteBrowserLogins(humanID, now string) (int, error) {
	var n int
	if err := t.queryRow("SELECT count(*) FROM browser_logins WHERE human_id = ? AND expires_at > ?", humanID, now).Scan(&n); err != nil {
		return 0, err
	}
	return n, t.exec("DELETE FROM browser_logins WHERE human_id = ?", humanID)
}

// DeleteExpiredBrowserLogins removes the browser logins that ended at or before now.
func (t *tx) DeleteExpiredBrowserLogins(now string) error {
	return t.exec("DELETE FROM browser_logins WHERE expires_at <= ?", now)
}

// HumanCount returns how many humans exist.
func (t *tx) HumanCount() (int, error) {
	var n int
	err := t.queryRow("SELECT count(*) FROM humans").Scan(&n)
	return n, err
}

const (
	boardInsertColumns = "id, name, title, template, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by, message_count, last_message_at, visibility, lifecycle, agents_add_people, task_prefix"
	boardColumns       = boardInsertColumns + ", (SELECT count(*) FROM tasks WHERE tasks.board_id = boards.id AND tasks.state = 'open')"
)

func scanBoard(row interface{ Scan(...any) error }) (board.Board, error) {
	var b board.Board
	var roles, policy string
	if err := row.Scan(&b.ID, &b.Name, &b.Title, &b.Template, &b.Charter, &roles, &policy, &b.HeadSeq, &b.HeadHash, &b.CreatedAt, &b.CreatedBy, &b.MessageCount, &b.LastMessageAt, &b.Visibility, &b.Lifecycle, &b.AgentsAddPeople, &b.TaskPrefix, &b.TasksOpen); err != nil {
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
	return t.exec("INSERT INTO boards ("+boardInsertColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		b.ID, b.Name, b.Title, b.Template, b.Charter, string(roles), string(policy), b.HeadSeq, b.HeadHash, b.CreatedAt, b.CreatedBy,
		b.MessageCount, b.LastMessageAt, b.Visibility, lifecycleDefault(b.Lifecycle), b.AgentsAddPeople, b.TaskPrefix)
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

// onBoard selects the ids of the boards a human is on.
const onBoard = "(SELECT board_id FROM members WHERE human_id = ? AND kind = 'human' AND status = 'active')"

// BoardsOfHuman lists the boards a human is on, by name.
func (t *tx) BoardsOfHuman(humanID string) ([]board.Board, error) {
	return t.boards("SELECT "+boardColumns+" FROM boards WHERE lifecycle != 'deleted' AND id IN "+onBoard+" ORDER BY name", humanID)
}

// BoardsSeenBy lists, by name, the open boards and the boards the human is on.
func (t *tx) BoardsSeenBy(humanID string) ([]board.Board, error) {
	return t.boards("SELECT "+boardColumns+" FROM boards WHERE lifecycle != 'deleted' AND (visibility = 'open' OR id IN "+onBoard+") ORDER BY name", humanID)
}

// PrivateBoardsNotOn lists, oldest first, the private boards the human isn't on.
func (t *tx) PrivateBoardsNotOn(humanID string) ([]board.Board, error) {
	return t.boards("SELECT "+boardColumns+" FROM boards WHERE lifecycle != 'deleted' AND visibility = 'private' AND id NOT IN "+onBoard+" ORDER BY created_at, id", humanID)
}

// SetBoardVisibility makes a board open or private.
func (t *tx) SetBoardVisibility(boardID, visibility string) error {
	return t.exec("UPDATE boards SET visibility = ? WHERE id = ?", visibility, boardID)
}

// BoardCreation returns who may create boards; with nothing set, every member may.
func (t *tx) BoardCreation() (string, error) {
	var v string
	err := t.queryRow("SELECT value FROM meta WHERE key = 'board_creation'").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return board.CreationMembers, nil
	}
	return v, err
}

// SetBoardCreation sets who may create boards.
func (t *tx) SetBoardCreation(v string) error {
	return t.exec("INSERT INTO meta (key, value) VALUES ('board_creation', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", v)
}

func (t *tx) boards(query string, args ...any) ([]board.Board, error) {
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
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
	memberInsertColumns = "id, board_id, name, kind, role, human_id, owner, harness, token_digest, key_id, access, status, cursor, joined_at, session"
	memberColumns       = memberInsertColumns + ", presence, presence_since, presence_at, delivery, delivery_setting, delivery_setting_seq, (SELECT role FROM humans WHERE humans.id = members.human_id), removed_at, removed_by, current_task_id, (SELECT ref FROM tasks WHERE tasks.id = members.current_task_id), (SELECT title FROM tasks WHERE tasks.id = members.current_task_id), (SELECT display_name FROM humans WHERE humans.id = members.human_id AND members.kind = 'human')"
)

func scanMember(row interface{ Scan(...any) error }) (board.Member, error) {
	var m board.Member
	var taskID, taskRef, taskTitle sql.NullString
	var access, presence, since, at, mode, setting sql.NullString
	err := row.Scan(&m.ID, &m.BoardID, &m.Name, &m.Kind, &m.Role, &m.HumanID, &m.Owner, &m.Harness, &m.TokenDigest, &m.KeyID, &access, &m.Status, &m.Cursor, &m.JoinedAt,
		&m.Session, &presence, &since, &at, &mode, &setting, &m.Delivery.Seq, &m.PersonRole, &m.RemovedAt, &m.RemovedBy, &taskID, &taskRef, &taskTitle, &m.DisplayName)
	if taskID.Valid {
		m.CurrentTask = &board.TaskRef{ID: taskID.String, Ref: taskRef.String, Title: taskTitle.String}
	}
	m.Access = access.String
	m.Presence = board.Presence{State: presence.String, Since: since.String, At: at.String, Delivery: mode.String}
	m.Delivery.Mode = setting.String
	if m.Kind == "agent" && m.Status == board.StatusLeft && m.RemovedAt != nil && m.RemovedBy == nil {
		// An agent that left is stored without removed_by, whose column predates the value.
		m.RemovedBy = ptrTo(board.RemovedBySelf)
	}
	return m, notFound(err)
}

func ptrTo(s string) *string { return &s }

// SetDelivery records an agent's delivery mode as its person set it, and the seq of the
// event that set it.
func (t *tx) SetDelivery(memberID string, d board.DeliverySetting) error {
	return t.exec("UPDATE members SET delivery_setting = ?, delivery_setting_seq = ? WHERE id = ?",
		sql.NullString{String: d.Mode, Valid: d.Mode != ""}, d.Seq, memberID)
}

// SetPresence records an agent's presence, when it began and when it was reported, and
// its delivery mode.
func (t *tx) SetPresence(memberID string, p board.Presence) error {
	return t.exec("UPDATE members SET presence = ?, presence_since = ?, presence_at = ?, delivery = ? WHERE id = ?",
		p.State, p.Since, p.At, sql.NullString{String: p.Delivery, Valid: p.Delivery != ""}, memberID)
}

// InsertMember adds a member.
func (t *tx) InsertMember(m board.Member) error {
	return t.exec("INSERT INTO members ("+memberInsertColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)",
		m.ID, m.BoardID, m.Name, m.Kind, m.Role, m.HumanID, m.Owner, m.Harness, m.TokenDigest, m.KeyID, m.Access, m.Status, m.Cursor, m.JoinedAt, m.Session)
}

// MemberByTokenDigest finds the agent whose token has this digest.
func (t *tx) MemberByTokenDigest(digest string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE token_digest = ?", digest))
}

// MemberByID finds a member of any board by id.
func (t *tx) MemberByID(id string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members WHERE id = ?", id))
}

// SeatForSession finds the human's newest agent on the board made for the session.
func (t *tx) SeatForSession(boardID, humanID, session string) (board.Member, error) {
	return scanMember(t.queryRow("SELECT "+memberColumns+" FROM members"+
		" WHERE board_id = ? AND human_id = ? AND kind = 'agent' AND session = ? ORDER BY joined_at DESC, rowid DESC LIMIT 1",
		boardID, humanID, session))
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

func (t *tx) StreamBoard(boardID string) (board.Board, error) {
	var b board.Board
	err := t.queryRow("SELECT id, name, head_seq, lifecycle FROM boards WHERE id = ?", boardID).
		Scan(&b.ID, &b.Name, &b.HeadSeq, &b.Lifecycle)
	return b, notFound(err)
}

func (t *tx) StreamMembers(boardID string) ([]board.Member, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT id, board_id, name, kind, human_id,
		status, cursor, COALESCE(presence, ''), COALESCE(presence_since, ''),
		COALESCE(presence_at, ''), COALESCE(delivery, '')
		FROM members WHERE board_id = ? ORDER BY rowid`, boardID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []board.Member
	for rows.Next() {
		var m board.Member
		if err := rows.Scan(&m.ID, &m.BoardID, &m.Name, &m.Kind, &m.HumanID,
			&m.Status, &m.Cursor, &m.Presence.State, &m.Presence.Since,
			&m.Presence.At, &m.Presence.Delivery); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMemberStatus sets whether a member is on its board: active, left or removed.
func (t *tx) SetMemberStatus(memberID, status string) error {
	return t.exec("UPDATE members SET status = ? WHERE id = ?", status, memberID)
}

// RemoveAgent marks an agent removed, with when and by whom. An agent that left by
// itself is stored as left, with no removed_by.
func (t *tx) RemoveAgent(memberID, at, by string) error {
	if by == board.RemovedBySelf {
		return t.exec("UPDATE members SET status = 'left', removed_at = ?, removed_by = NULL WHERE id = ? AND kind = 'agent'", at, memberID)
	}
	return t.exec("UPDATE members SET status = 'removed', removed_at = ?, removed_by = ? WHERE id = ? AND kind = 'agent'", at, by, memberID)
}

// SetAgentToken replaces an agent's token and the key it stops with.
func (t *tx) SetAgentToken(memberID, digest, keyID string) error {
	return t.exec("UPDATE members SET token_digest = ?, key_id = ? WHERE id = ? AND kind = 'agent'", digest, keyID, memberID)
}

const delegationColumns = "id, key_id, human_id, name, digest, created_at, ended_at"

func scanDelegation(row interface{ Scan(...any) error }) (board.Delegation, error) {
	var d board.Delegation
	err := row.Scan(&d.ID, &d.KeyID, &d.HumanID, &d.Name, &d.Digest, &d.CreatedAt, &d.EndedAt)
	return d, notFound(err)
}

// InsertDelegation adds a machine delegation.
func (t *tx) InsertDelegation(d board.Delegation) error {
	return t.exec("INSERT INTO delegations ("+delegationColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		d.ID, d.KeyID, d.HumanID, d.Name, d.Digest, d.CreatedAt, d.EndedAt)
}

// EndDelegations ends the key's delegations under the name that haven't ended.
func (t *tx) EndDelegations(keyID, name, at string) error {
	return t.exec("UPDATE delegations SET ended_at = ? WHERE key_id = ? AND name = ? AND ended_at IS NULL", at, keyID, name)
}

// DelegationByDigest finds a machine delegation by the digest of its token.
func (t *tx) DelegationByDigest(digest string) (board.Delegation, error) {
	return scanDelegation(t.queryRow("SELECT "+delegationColumns+" FROM delegations WHERE digest = ?", digest))
}

// DelegationByID finds a machine delegation by id.
func (t *tx) DelegationByID(id string) (board.Delegation, error) {
	return scanDelegation(t.queryRow("SELECT "+delegationColumns+" FROM delegations WHERE id = ?", id))
}

// SetMemberAccess sets what a person may change on their board: admin or member.
func (t *tx) SetMemberAccess(memberID, access string) error {
	return t.exec("UPDATE members SET access = ? WHERE id = ? AND kind = 'human'", access, memberID)
}

// SetCursor moves a member's read position forward; it never moves it back.
func (t *tx) SetCursor(memberID string, seq int64) error {
	return t.exec("UPDATE members SET cursor = max(cursor, ?) WHERE id = ?", seq, memberID)
}

const joinCodeColumns = "id, board_id, code_digest, role, expires_at, created_at, created_by, revoked_at, kind, guest, guest_id, used_at, used_by"

func scanJoinCode(row interface{ Scan(...any) error }) (board.JoinCode, error) {
	var j board.JoinCode
	err := row.Scan(&j.ID, &j.BoardID, &j.CodeDigest, &j.Role, &j.ExpiresAt, &j.CreatedAt, &j.CreatedBy, &j.RevokedAt, &j.Kind, &j.Guest, &j.GuestID, &j.UsedAt, &j.UsedBy)
	return j, notFound(err)
}

// InsertJoinCode adds a join code; a code with no kind is a pairing code.
func (t *tx) InsertJoinCode(j board.JoinCode) error {
	if j.Kind == "" {
		j.Kind = board.CodePairing
	}
	return t.exec("INSERT INTO join_codes ("+joinCodeColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		j.ID, j.BoardID, j.CodeDigest, j.Role, j.ExpiresAt, j.CreatedAt, j.CreatedBy, j.RevokedAt, j.Kind, j.Guest, j.GuestID, j.UsedAt, j.UsedBy)
}

// UseJoinCode marks an unused join code used, and reports whether it was unused.
func (t *tx) UseJoinCode(id, at, memberID string) (bool, error) {
	res, err := t.tx.ExecContext(t.ctx, "UPDATE join_codes SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL", at, memberID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// JoinCodeByDigest finds a join code by the digest of the code.
func (t *tx) JoinCodeByDigest(digest string) (board.JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE code_digest = ?", digest))
}

// JoinCodeByID finds a join code by id.
func (t *tx) JoinCodeByID(id string) (board.JoinCode, error) {
	return scanJoinCode(t.queryRow("SELECT "+joinCodeColumns+" FROM join_codes WHERE id = ?", id))
}

// WorkingJoinCodes lists a board's join codes that are neither revoked, used nor expired
// at now, oldest first.
func (t *tx) WorkingJoinCodes(boardID, now string) ([]board.JoinCode, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+joinCodeColumns+" FROM join_codes WHERE board_id = ? AND revoked_at IS NULL AND used_at IS NULL AND expires_at > ? ORDER BY rowid", boardID, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	var out []board.JoinCode
	for rows.Next() {
		j, err := scanJoinCode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
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

// PersonAdded finds the latest person.added event for a person's member on a board.
func (t *tx) PersonAdded(boardID, memberID string) (events.Event, error) {
	var e events.Event
	var actor string
	err := t.queryRow("SELECT seq, at, actor_json FROM events WHERE board_id = ? AND type = ? AND json_extract(data_json, '$.member_id') = ? ORDER BY seq DESC LIMIT 1",
		boardID, events.PersonAdded, memberID).Scan(&e.Seq, &e.At, &actor)
	if err != nil {
		return events.Event{}, notFound(err)
	}
	e.BoardID, e.Type = boardID, events.PersonAdded
	return e, json.Unmarshal([]byte(actor), &e.Actor)
}

func lifecycleDefault(v string) string {
	if v == "" {
		return board.LifecycleActive
	}
	return v
}

// SetBoardLifecycle changes only access state, retaining all rows and the name.
func (t *tx) SetBoardLifecycle(id, lifecycle string) error {
	return t.exec("UPDATE boards SET lifecycle = ? WHERE id = ?", lifecycle, id)
}

// ReservedHandle identifies the person to whom a renamed handle belongs forever.
func (t *tx) ReservedHandle(name string) (string, error) {
	var id string
	err := t.queryRow("SELECT human_id FROM person_handles WHERE handle = ?", name).Scan(&id)
	return id, notFound(err)
}

// MembershipBoards includes former memberships; deleted records cannot receive events.
func (t *tx) MembershipBoards(id string) ([]board.Board, error) {
	return t.boards("SELECT "+boardColumns+" FROM boards WHERE lifecycle != 'deleted' AND id IN (SELECT board_id FROM members WHERE human_id = ? AND kind = 'human') ORDER BY id", id)
}

// RenameHuman reserves both handles and changes all member projections atomically.
func (t *tx) RenameHuman(id, name string) error {
	h, err := t.HumanByID(id)
	if err != nil {
		return err
	}
	if owner, err := t.ReservedHandle(name); err == nil && owner != id {
		return board.HandleTaken(name)
	} else if err != nil && !errors.Is(err, board.ErrNotFound) {
		return err
	}
	if other, err := t.HumanByName(name); err == nil && other.ID != id {
		return board.HandleTaken(name)
	} else if err != nil && !errors.Is(err, board.ErrNotFound) {
		return err
	}
	var collisions int
	if err := t.queryRow("SELECT count(*) FROM members x WHERE x.name = ? AND x.human_id != ? AND x.board_id IN (SELECT board_id FROM members WHERE human_id = ? AND kind = 'human')", name, id, id).Scan(&collisions); err != nil {
		return err
	}
	// An agent of this person also occupies a distinct member name.
	var ownAgents int
	if err := t.queryRow("SELECT count(*) FROM members x WHERE x.name = ? AND x.kind != 'human' AND x.board_id IN (SELECT board_id FROM members WHERE human_id = ? AND kind = 'human')", name, id).Scan(&ownAgents); err != nil {
		return err
	}
	if collisions+ownAgents > 0 {
		return board.HandleTaken(name)
	}
	for _, handle := range []string{h.Name, name} {
		if err := t.exec("INSERT INTO person_handles(handle, human_id) VALUES (?, ?) ON CONFLICT(handle) DO NOTHING", handle, id); err != nil {
			return err
		}
	}
	if err := t.exec("UPDATE humans SET name = ? WHERE id = ?", name, id); err != nil {
		return err
	}
	if err := t.exec("UPDATE members SET name = ? WHERE human_id = ? AND kind = 'human'", name, id); err != nil {
		return err
	}
	return t.exec("UPDATE members SET owner = ? WHERE human_id = ? AND kind = 'agent'", name, id)
}
