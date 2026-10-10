package sqlite

import "github.com/leonidas1712/aboard/server/internal/board"

func (t *tx) ApprovalOutcome(id string) (board.ApprovalOutcomeRecord, error) {
	var out board.ApprovalOutcomeRecord
	err := t.queryRow("SELECT approval_id,invite_id,version,consumed_at FROM admin_approval_outcomes WHERE approval_id=?", id).Scan(&out.ApprovalID, &out.InviteID, &out.Version, &out.ConsumedAt)
	return out, notFound(err)
}

func (t *tx) InsertApprovalOutcome(out board.ApprovalOutcomeRecord) error {
	return t.exec("INSERT INTO admin_approval_outcomes(approval_id,invite_id,version) VALUES(?,?,?)", out.ApprovalID, out.InviteID, out.Version)
}

func (t *tx) ConsumeApprovalOutcome(id, at string) (bool, error) {
	result, err := t.tx.ExecContext(t.ctx, "UPDATE admin_approval_outcomes SET consumed_at=? WHERE approval_id=? AND consumed_at IS NULL", at, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
