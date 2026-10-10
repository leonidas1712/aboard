package sqlite

import (
	"encoding/json"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// SaveOnboardingReceipt saves an immutable redemption outcome in its transaction.
func (t *tx) SaveOnboardingReceipt(r board.OnboardingReceipt) error {
	boards, err := json.Marshal(r.Boards)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO onboarding_receipts (key_id, person_id, invite_id, server_id, handle, boards, pairing_request_id) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''))", r.KeyID, r.PersonID, r.InviteID, r.ServerID, r.Handle, boards, r.PairingRequestID)
}

// OnboardingReceipt looks up the original outcome by its exact access key.
func (t *tx) OnboardingReceipt(keyID string) (board.OnboardingReceipt, error) {
	var r board.OnboardingReceipt
	var boards string
	var pairing *string
	err := t.queryRow("SELECT key_id, person_id, invite_id, server_id, handle, boards, pairing_request_id FROM onboarding_receipts WHERE key_id = ?", keyID).Scan(&r.KeyID, &r.PersonID, &r.InviteID, &r.ServerID, &r.Handle, &boards, &pairing)
	if err != nil {
		return r, notFound(err)
	}
	if pairing != nil {
		r.PairingRequestID = *pairing
	}
	err = json.Unmarshal([]byte(boards), &r.Boards)
	return r, err
}

// ServerInviteByID finds nonsecret metadata by immutable invite id.
func (t *tx) ServerInviteByID(id string) (board.ServerInvite, error) {
	return scanServerInvite(t.queryRow("SELECT "+serverInviteColumns+" FROM server_invites WHERE id = ?", id))
}

// ServerInvites lists the person's invitations, including those issued by their agents.
func (t *tx) ServerInvites(personID string) ([]board.ServerInvite, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+serverInviteColumns+" FROM server_invites WHERE created_by = ? ORDER BY created_at, id", personID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []board.ServerInvite{}
	for rows.Next() {
		i, err := scanServerInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeServerInvite marks an invitation revoked once.
func (t *tx) RevokeServerInvite(id, at string) (bool, error) {
	result, err := t.tx.ExecContext(t.ctx, "UPDATE server_invites SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", at, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		if err = t.clearInviteOutcome(id); err != nil {
			return false, err
		}
	}
	return n == 1, nil
}
