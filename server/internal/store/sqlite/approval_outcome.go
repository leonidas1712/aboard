package sqlite

import "github.com/leonidas1712/aboard/server/internal/board"

func (t *tx) ApprovalOutcome(id string) (board.ApprovalOutcomeRecord, error) {
	var out board.ApprovalOutcomeRecord
	var key *string
	err := t.queryRow("SELECT approval_id,invite_id,version,capsule,expires_at,winning_key_hash,consumed_at FROM admin_approval_outcomes WHERE approval_id=?", id).Scan(&out.ApprovalID, &out.InviteID, &out.Version, &out.Capsule, &out.ExpiresAt, &key, &out.ConsumedAt)
	if key != nil {
		out.WinningKeyHash = *key
	}
	return out, notFound(err)
}
func (t *tx) InsertApprovalOutcome(out board.ApprovalOutcomeRecord) error {
	return t.exec("INSERT INTO admin_approval_outcomes(approval_id,invite_id,version,capsule,expires_at) VALUES(?,?,?,?,?)", out.ApprovalID, out.InviteID, out.Version, out.Capsule, out.ExpiresAt)
}
func (t *tx) ConsumeApprovalOutcome(id, at, keyHash, until string) (bool, error) {
	result, err := t.tx.ExecContext(t.ctx, "UPDATE admin_approval_outcomes SET consumed_at=?,winning_key_hash=NULLIF(?,''),expires_at=?,capsule=CASE WHEN ?='' THEN NULL ELSE capsule END WHERE approval_id=? AND consumed_at IS NULL", at, keyHash, until, keyHash, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (t *tx) ClearApprovalOutcome(id string) error {
	return t.exec("UPDATE admin_approval_outcomes SET capsule=NULL,winning_key_hash=NULL WHERE approval_id=? AND capsule IS NOT NULL", id)
}
func (t *tx) clearInviteOutcome(inviteID string) error {
	return t.exec("UPDATE admin_approval_outcomes SET capsule=NULL,winning_key_hash=NULL WHERE invite_id=? AND capsule IS NOT NULL", inviteID)
}

func (t *tx) clearApprovalCapsules(idsSQL string, args ...any) error {
	return t.exec("UPDATE admin_approval_outcomes SET capsule=NULL,winning_key_hash=NULL WHERE capsule IS NOT NULL AND approval_id IN ("+idsSQL+")", args...)
}

func (t *tx) purgeApprovalCapsules(now string) error {
	return t.exec(`UPDATE admin_approval_outcomes SET capsule=NULL,winning_key_hash=NULL
 WHERE capsule IS NOT NULL AND (expires_at<=? OR invite_id IN
 (SELECT i.id FROM server_invites i
 LEFT JOIN humans h ON h.id=i.created_by
 LEFT JOIN members m ON m.id=i.issuing_agent_id
 LEFT JOIN access_keys k ON k.id=i.parent_key_id
 WHERE i.used_at IS NOT NULL OR i.revoked_at IS NOT NULL OR i.expires_at<=?
 OR h.id IS NULL OR h.role!='admin' OR h.removed_at IS NOT NULL
 OR m.id IS NULL OR m.status!='active' OR m.human_id!=i.created_by OR m.key_id!=i.parent_key_id
 OR k.id IS NULL OR k.human_id!=i.created_by OR k.revoked_at IS NOT NULL OR k.expires_at<=?
 OR NOT EXISTS (SELECT 1 FROM members hm WHERE hm.board_id=m.board_id AND hm.human_id=h.id AND hm.kind='human' AND hm.status='active')
 OR EXISTS (SELECT 1 FROM json_each(i.boards) j LEFT JOIN boards b ON b.id=j.value WHERE b.id IS NULL OR b.lifecycle!='active'
 OR NOT EXISTS (SELECT 1 FROM members hm WHERE hm.board_id=j.value AND hm.human_id=h.id AND hm.kind='human' AND hm.status='active'))))`, now, now, now)
}
