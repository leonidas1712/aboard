package sqlitejournal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// SaveNotice preserves the first immutable origin for a source notice.
func (j *Journal) SaveNotice(ctx context.Context, n delivery.DurableNotice) error {
	if n.ID == "" || (n.Kind != "approval_outcome" && n.Kind != "colleague_arrival") || n.SourceID == "" || n.Agent.Server == "" || n.Agent.MemberID == "" || n.Agent.Board == "" || n.Session.Harness == "" || n.Session.ID == "" || n.Boot == "" || n.Generation == 0 || n.Handed || n.Cancelled {
		return fmt.Errorf("invalid durable notice origin")
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return j.write(ctx, func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT state FROM durable_notices WHERE id=?`, n.ID).Scan(&existing)
		if err == nil {
			var old delivery.DurableNotice
			if err := json.Unmarshal([]byte(existing), &old); err != nil {
				return err
			}
			if old != n {
				return fmt.Errorf("notice origin changed")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO durable_notices(id,server,member_id,state) VALUES(?,?,?,?)`, n.ID, n.Agent.Server, n.Agent.MemberID, string(raw))
		return err
	})
}

// Notices reads the owned seat's pending and terminal notice records.
func (j *Journal) Notices(ctx context.Context, a delivery.AgentRef) ([]delivery.DurableNotice, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT state,handed,cancelled FROM durable_notices WHERE server=? AND member_id=? ORDER BY id`, a.Server, a.MemberID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.DurableNotice
	for rows.Next() {
		var raw string
		var n delivery.DurableNotice
		if err := rows.Scan(&raw, &n.Handed, &n.Cancelled); err != nil {
			return nil, err
		}
		handed, cancelled := n.Handed, n.Cancelled
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			return nil, err
		}
		n.Handed, n.Cancelled = handed, cancelled
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNoticeHanded records transport acceptance, not reading or delivery proof.
func (j *Journal) MarkNoticeHanded(ctx context.Context, id string) error {
	return j.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE durable_notices SET handed=1 WHERE id=? AND cancelled=0`, id)
		return err
	})
}

// CancelNotice removes a pending notice after fresh authority is refused.
func (j *Journal) CancelNotice(ctx context.Context, id string) error {
	return j.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE durable_notices SET cancelled=1 WHERE id=? AND handed=0`, id)
		return err
	})
}
