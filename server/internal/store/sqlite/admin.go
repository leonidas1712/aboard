package sqlite

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/events"
)

// Allowance reads a person's stable allowance identity and current categories.
func (t *tx) Allowance(personID string) (board.Allowance, error) {
	var a board.Allowance
	var raw string
	err := t.queryRow("SELECT id,person_id,revision,categories FROM admin_allowances WHERE person_id=?", personID).Scan(&a.ID, &a.PersonID, &a.Revision, &raw)
	if err != nil {
		return a, notFound(err)
	}
	return a, json.Unmarshal([]byte(raw), &a.Categories)
}

// SaveAllowance keeps the identity while replacing its categories and revision.
func (t *tx) SaveAllowance(a board.Allowance) error {
	raw, err := json.Marshal(a.Categories)
	if err != nil {
		return err
	}
	return t.exec("INSERT INTO admin_allowances(id,person_id,revision,categories) VALUES(?,?,?,?) ON CONFLICT(person_id) DO UPDATE SET revision=excluded.revision,categories=excluded.categories", a.ID, a.PersonID, a.Revision, string(raw))
}

const approvalColumns = "id,person_id,agent_id,parent_key_id,request_key,payload_hash,action,state,created_at,expires_at,decided_at,execution"

func scanApproval(row interface{ Scan(...any) error }) (board.Approval, error) {
	var a board.Approval
	var key, execution *string
	var action string
	err := row.Scan(&a.ID, &a.PersonID, &a.AgentID, &a.ParentKeyID, &key, &a.PayloadHash, &action, &a.State, &a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &execution)
	if err != nil {
		return a, notFound(err)
	}
	if key != nil {
		a.RequestKey = *key
	}
	if a.Action, err = board.AdminActionFromJSON([]byte(action)); err != nil {
		return a, err
	}
	if execution != nil {
		if err := json.Unmarshal([]byte(*execution), &a.Execution); err != nil {
			return a, err
		}
	}
	return a, nil
}

// Approval reads an exact request, its terminal decision and any immutable execution.
func (t *tx) Approval(id string) (board.Approval, error) {
	return scanApproval(t.queryRow("SELECT "+approvalColumns+" FROM admin_approvals WHERE id=?", id))
}

// ApprovalByRequest resolves an agent's retained retry key without returning secrets.
func (t *tx) ApprovalByRequest(agentID, key string) (board.Approval, error) {
	return scanApproval(t.queryRow("SELECT "+approvalColumns+" FROM admin_approvals WHERE agent_id=? AND request_key=?", agentID, key))
}

// Approvals lists a person's requests, with pending requests first.
func (t *tx) Approvals(personID string) ([]board.Approval, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT "+approvalColumns+" FROM admin_approvals WHERE person_id=? ORDER BY CASE state WHEN 'pending' THEN 0 ELSE 1 END,created_at,id", personID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []board.Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveApproval inserts a frozen request or decides a pending request without rewriting its payload.
func (t *tx) SaveApproval(a board.Approval) error {
	raw, err := events.Canonical(board.AdminActionJSON(a.Action))
	if err != nil {
		return err
	}
	var key, execution *string
	if a.RequestKey != "" {
		key = &a.RequestKey
	}
	if a.Execution != nil {
		v, err := json.Marshal(a.Execution)
		if err != nil {
			return err
		}
		s := string(v)
		execution = &s
	}
	existing, err := t.Approval(a.ID)
	if err == nil {
		if existing.State != "pending" {
			return fmt.Errorf("terminal approval is immutable")
		}
		if existing.PayloadHash != a.PayloadHash || existing.AgentID != a.AgentID || existing.PersonID != a.PersonID || existing.ParentKeyID != a.ParentKeyID {
			return fmt.Errorf("approval identity is immutable")
		}
		return t.exec("UPDATE admin_approvals SET state=?,decided_at=?,execution=? WHERE id=? AND state='pending'", a.State, a.DecidedAt, execution, a.ID)
	}
	if !errors.Is(err, board.ErrNotFound) {
		return err
	}
	return t.exec("INSERT INTO admin_approvals("+approvalColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", a.ID, a.PersonID, a.AgentID, a.ParentKeyID, key, a.PayloadHash, string(raw), a.State, a.CreatedAt, a.ExpiresAt, a.DecidedAt, execution)
}

// ExpireAdminRequestKeys releases retry keys without altering immutable audit records.
func (t *tx) ExpireAdminRequestKeys(before string) error {
	return t.exec("UPDATE admin_approvals SET request_key=NULL WHERE request_key IS NOT NULL AND created_at<=?", before)
}
