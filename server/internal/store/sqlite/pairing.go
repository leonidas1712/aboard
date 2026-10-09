package sqlite

import (
	"encoding/json"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (t *tx) PairingByID(id string) (board.PairingRequest, error) {
	var r board.PairingRequest
	var data []byte
	if err := t.queryRow("SELECT data FROM pairing_requests WHERE id = ?", id).Scan(&data); err != nil {
		return r, notFound(err)
	}
	err := json.Unmarshal(data, &r)
	return r, err
}

func (t *tx) PairingsOf(personID string) ([]board.PairingRequest, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT data FROM pairing_requests WHERE inviter_id = ? OR recipient_id = ? ORDER BY created_at, id", personID, personID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []board.PairingRequest{}
	for rows.Next() {
		var data []byte
		var r board.PairingRequest
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *tx) SavePairing(r board.PairingRequest) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO pairing_requests (id, invite_id, inviter_id, recipient_id, created_at, creation_scope, creation_key, data) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET recipient_id = excluded.recipient_id, data = excluded.data", r.ID, r.InviteID, r.InviterID, r.RecipientID, r.CreatedAt, r.Creation.Scope, r.Creation.Key, data)
}

func (t *tx) PairingCredentialByDigest(digest string) (board.PairingCredential, error) {
	var c board.PairingCredential
	var data []byte
	if err := t.queryRow("SELECT data FROM pairing_credentials WHERE digest = ?", digest).Scan(&data); err != nil {
		return c, notFound(err)
	}
	err := json.Unmarshal(data, &c)
	return c, err
}

func (t *tx) SavePairingCredential(c board.PairingCredential) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO pairing_credentials (id, request_id, digest, data) VALUES (?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data", c.ID, c.RequestID, c.Digest, data)
}

func (t *tx) DeletePairingCredentials(id string) error {
	return t.exec("DELETE FROM pairing_credentials WHERE request_id = ?", id)
}

func (t *tx) PairingByInvite(inviteID string) (board.PairingRequest, error) {
	var r board.PairingRequest
	var data []byte
	if err := t.queryRow("SELECT data FROM pairing_requests WHERE invite_id = ?", inviteID).Scan(&data); err != nil {
		return r, notFound(err)
	}
	err := json.Unmarshal(data, &r)
	return r, err
}

func (t *tx) PairingByCreation(scope, key string) (board.PairingRequest, error) {
	var r board.PairingRequest
	var data []byte
	if err := t.queryRow("SELECT data FROM pairing_requests WHERE creation_scope = ? AND creation_key = ? ORDER BY created_at DESC, rowid DESC LIMIT 1", scope, key).Scan(&data); err != nil {
		return r, notFound(err)
	}
	err := json.Unmarshal(data, &r)
	return r, err
}
