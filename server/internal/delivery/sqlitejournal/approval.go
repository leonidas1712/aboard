package sqlitejournal

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// SaveApprovalWatch refuses to move an existing origin or save a stale binding.
func (j *Journal) SaveApprovalWatch(ctx context.Context, w delivery.ApprovalWatch) error {
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM bindings b JOIN sessions s ON s.harness=b.harness AND s.session_id=b.session_id WHERE b.server=? AND b.member_id=? AND b.generation=? AND b.harness=? AND b.session_id=? AND s.boot=? AND s.open=1`, w.Agent.Server, w.Agent.MemberID, w.Generation, w.Session.Harness, w.Session.ID, w.Boot).Scan(&current)
	if err != nil {
		return err
	}
	if current != 1 {
		return fmt.Errorf("approval watch has no current binding")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO approval_watches(server,approval_id,member_id,harness,session_id,boot,generation,state) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(server,approval_id) DO UPDATE SET state=excluded.state WHERE approval_watches.member_id=excluded.member_id AND approval_watches.harness=excluded.harness AND approval_watches.session_id=excluded.session_id AND approval_watches.boot=excluded.boot AND approval_watches.generation=excluded.generation AND approval_watches.invalidated=0`, w.Agent.Server, w.ID, w.Agent.MemberID, w.Session.Harness, w.Session.ID, w.Boot, w.Generation, string(raw))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("approval origin is already retained")
	}
	return tx.Commit()
}

// ApprovalWatches loads nonsecret origins for one exact harness session.
func (j *Journal) ApprovalWatches(ctx context.Context, key delivery.SessionKey) ([]delivery.ApprovalWatch, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT state FROM approval_watches WHERE harness=? AND session_id=? AND invalidated=0 ORDER BY server,approval_id`, key.Harness, key.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var watches []delivery.ApprovalWatch
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var w delivery.ApprovalWatch
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		watches = append(watches, w)
	}
	return watches, rows.Err()
}

// ApprovalSeatWatches exposes retained origins only for an explicitly bound permanent seat.
// Invalidated origins are metadata for notices, never endpoint or collection authority.
func (j *Journal) ApprovalSeatWatches(ctx context.Context, agent delivery.AgentRef) ([]delivery.ApprovalWatch, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT state FROM approval_watches WHERE server=? AND member_id=? ORDER BY approval_id`, agent.Server, agent.MemberID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.ApprovalWatch
	for rows.Next() {
		var raw string
		var w delivery.ApprovalWatch
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
