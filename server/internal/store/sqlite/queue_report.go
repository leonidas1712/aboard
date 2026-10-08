package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func (t *tx) QueueReport(memberID string) (board.QueueReport, error) {
	var q board.QueueReport
	var ids string
	err := t.queryRow("SELECT member_id, board_id, session, boot, credential_digest, epoch, revision, expires_at, messages FROM delivery_queue_reports WHERE member_id = ?", memberID).Scan(&q.MemberID, &q.BoardID, &q.Session, &q.Boot, &q.CredentialDigest, &q.Epoch, &q.Revision, &q.ExpiresAt, &ids)
	if err != nil {
		return q, notFound(err)
	}
	if err = json.Unmarshal([]byte(ids), &q.Messages); err != nil {
		return q, fmt.Errorf("decode queue identities: %w", err)
	}
	return q, nil
}

func (t *tx) SaveQueueReport(q board.QueueReport) error {
	ids, err := json.Marshal(q.Messages)
	if err != nil {
		return err
	}
	return t.exec(`INSERT INTO delivery_queue_reports(member_id,board_id,session,boot,credential_digest,epoch,revision,expires_at,messages) VALUES (?,?,?,?,?,?,?,?,?)
 ON CONFLICT(member_id) DO UPDATE SET board_id=excluded.board_id,session=excluded.session,boot=excluded.boot,credential_digest=excluded.credential_digest,epoch=excluded.epoch,revision=excluded.revision,expires_at=excluded.expires_at,messages=excluded.messages`, q.MemberID, q.BoardID, q.Session, q.Boot, q.CredentialDigest, q.Epoch, q.Revision, q.ExpiresAt, string(ids))
}
