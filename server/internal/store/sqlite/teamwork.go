package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// SetBoardAgentsAddPeople changes only the board's teammate-add gate.
func (t *tx) SetBoardAgentsAddPeople(id string, allowed bool) error {
	return t.exec("UPDATE boards SET agents_add_people = ? WHERE id = ?", allowed, id)
}

// AgentsAddPeople reads the server's gate, enabled unless explicitly disabled.
func (t *tx) AgentsAddPeople() (bool, error) {
	var value string
	err := t.queryRow("SELECT COALESCE((SELECT value FROM meta WHERE key = 'agents_add_people'), 'true')").Scan(&value)
	return value == "true", err
}

// SetAgentsAddPeople changes the server-wide gate.
func (t *tx) SetAgentsAddPeople(allowed bool) error {
	return t.exec("INSERT INTO meta (key,value) VALUES ('agents_add_people',?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", fmt.Sprint(allowed))
}

// DelegatedCreation reads a creation receipt without deciding whether it can be replayed.
func (t *tx) DelegatedCreation(delegationID, key string) (board.CreationReceipt, error) {
	r := board.CreationReceipt{DelegationID: delegationID, Key: key}
	var result string
	if err := t.queryRow("SELECT request_hash, created_at, result_json FROM delegated_creations WHERE delegation_id = ? AND key = ?", delegationID, key).Scan(&r.RequestHash, &r.CreatedAt, &result); err != nil {
		return board.CreationReceipt{}, notFound(err)
	}
	if err := json.Unmarshal([]byte(result), &r.Joined); err != nil {
		return board.CreationReceipt{}, fmt.Errorf("read delegated creation result: %w", err)
	}
	return r, nil
}

// SaveDelegatedCreation records the result in the board and seat's transaction.
func (t *tx) SaveDelegatedCreation(r board.CreationReceipt) error {
	result, err := json.Marshal(r.Joined)
	if err != nil {
		return fmt.Errorf("encode delegated creation result: %w", err)
	}
	return t.exec("INSERT INTO delegated_creations (delegation_id,key,request_hash,created_at,result_json) VALUES (?,?,?,?,?) ON CONFLICT (delegation_id,key) DO UPDATE SET request_hash = excluded.request_hash, created_at = excluded.created_at, result_json = excluded.result_json", r.DelegationID, r.Key, r.RequestHash, r.CreatedAt, string(result))
}
