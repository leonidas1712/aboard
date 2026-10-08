package sqlitejournal

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// SaveShown atomically records exact identities against the current binding and boot.
func (j *Journal) SaveShown(ctx context.Context, records []delivery.ShownRecord) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range records {
		var current int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bindings b JOIN sessions s ON s.harness=b.harness AND s.session_id=b.session_id WHERE b.server=? AND b.member_id=? AND b.generation=? AND b.harness=? AND b.session_id=? AND s.boot=? AND s.open=1`, r.Agent.Server, r.Agent.MemberID, r.Generation, r.Session.Harness, r.Session.ID, r.Boot).Scan(&current)
		if err != nil {
			return err
		}
		if current != 1 {
			return fmt.Errorf("shown observation has no current session binding")
		}
		if r.Message.BoardID == "" || r.Message.MemberID != r.Agent.MemberID || r.Message.MessageID == "" || r.Message.Seq <= 0 {
			return fmt.Errorf("invalid shown identity")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO shown_messages(harness,session_id,boot,server,member_id,generation,board_id,message_id,seq) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, r.Session.Harness, r.Session.ID, r.Boot, r.Agent.Server, r.Agent.MemberID, r.Generation, r.Message.BoardID, r.Message.MessageID, r.Message.Seq)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ShownMessages loads observations for one boot without storing message bodies.
func (j *Journal) ShownMessages(ctx context.Context, key delivery.SessionKey, boot string) ([]delivery.ShownRecord, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT server,member_id,generation,board_id,message_id,seq FROM shown_messages WHERE harness=? AND session_id=? AND boot=?`, key.Harness, key.ID, boot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.ShownRecord
	for rows.Next() {
		r := delivery.ShownRecord{Session: key, Boot: boot}
		if err := rows.Scan(&r.Agent.Server, &r.Agent.MemberID, &r.Generation, &r.Message.BoardID, &r.Message.MessageID, &r.Message.Seq); err != nil {
			return nil, err
		}
		r.Message.MemberID = r.Agent.MemberID
		out = append(out, r)
	}
	return out, rows.Err()
}
