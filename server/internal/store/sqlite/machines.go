package sqlite

import (
	"database/sql"

	"github.com/leonidas1712/aboard/server/internal/board"
)

const machineRequestColumns = "id, code_digest, secret_digest, label, handle, requested_from, created_at, expires_at, state, decided_by, decided_key, decided_at, key_id, polls"

func scanMachineRequest(row *sql.Row) (board.MachineRequest, error) {
	var r board.MachineRequest
	err := row.Scan(&r.ID, &r.CodeDigest, &r.SecretDigest, &r.Label, &r.Handle, &r.RequestedFrom, &r.CreatedAt, &r.ExpiresAt, &r.State,
		&r.DecidedBy, &r.DecidedKey, &r.DecidedAt, &r.KeyID, &r.Polls)
	return r, notFound(err)
}

// InsertMachineRequest adds a machine request.
func (t *tx) InsertMachineRequest(r board.MachineRequest) error {
	return t.exec("INSERT INTO machine_requests ("+machineRequestColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.CodeDigest, r.SecretDigest, r.Label, r.Handle, r.RequestedFrom, r.CreatedAt, r.ExpiresAt, r.State,
		r.DecidedBy, r.DecidedKey, r.DecidedAt, r.KeyID, r.Polls)
}

// MachineRequestByCode finds a machine request by the digest of its short code.
func (t *tx) MachineRequestByCode(digest string) (board.MachineRequest, error) {
	return scanMachineRequest(t.queryRow("SELECT "+machineRequestColumns+" FROM machine_requests WHERE code_digest = ?", digest))
}

// MachineRequestBySecret finds a machine request by the digest of its collection secret.
func (t *tx) MachineRequestBySecret(digest string) (board.MachineRequest, error) {
	return scanMachineRequest(t.queryRow("SELECT "+machineRequestColumns+" FROM machine_requests WHERE secret_digest = ?", digest))
}

// DeleteEndedMachineRequests removes the machine requests that expired at or before now.
func (t *tx) DeleteEndedMachineRequests(now string) error {
	return t.exec("DELETE FROM machine_requests WHERE expires_at <= ?", now)
}

// DecideMachineRequest approves or refuses a pending machine request, and reports
// whether it was pending.
func (t *tx) DecideMachineRequest(id, state, humanID, keyID, at string) (bool, error) {
	return t.changedOne("UPDATE machine_requests SET state = ?, decided_by = ?, decided_key = ?, decided_at = ? WHERE id = ? AND state = 'pending'",
		state, humanID, keyID, at, id)
}

// CountMachineRequestPoll counts one more collection attempt on a machine request.
func (t *tx) CountMachineRequestPoll(id string) error {
	return t.exec("UPDATE machine_requests SET polls = polls + 1 WHERE id = ?", id)
}

// CollectMachineRequest marks an approved machine request collected with its key, and
// reports whether it was approved and not yet collected.
func (t *tx) CollectMachineRequest(id, keyID string) (bool, error) {
	return t.changedOne("UPDATE machine_requests SET state = 'collected', key_id = ? WHERE id = ? AND state = 'approved'", keyID, id)
}

// changedOne runs an update and reports whether it changed exactly one row.
func (t *tx) changedOne(query string, args ...any) (bool, error) {
	res, err := t.tx.ExecContext(t.ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
