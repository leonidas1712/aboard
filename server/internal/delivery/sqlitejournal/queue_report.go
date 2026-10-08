package sqlitejournal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// QueueReporter loads a retained reporter for one current binding generation.
func (j *Journal) QueueReporter(ctx context.Context, agent delivery.AgentRef, key delivery.SessionKey, boot string, generation uint64) (delivery.QueueReporter, error) {
	fresh := delivery.QueueReporter{Agent: agent, Session: key, Boot: boot, Generation: generation}
	var raw string
	err := j.db.QueryRowContext(ctx, `SELECT state FROM queue_reporters WHERE server=? AND member_id=? AND harness=? AND session_id=? AND boot=? AND generation=?`, agent.Server, agent.MemberID, key.Harness, key.ID, boot, generation).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fresh, nil
	}
	if err != nil {
		return fresh, err
	}
	var state delivery.QueueReporter
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return fresh, err
	}
	if state.Agent.Key() != agent.Key() || state.Session != key || state.Boot != boot || state.Generation != generation {
		return fresh, fmt.Errorf("queue reporter identity changed")
	}
	state.Agent = agent
	return state, nil
}

// SaveQueueReporter records its immutable pending request before network publication.
func (j *Journal) SaveQueueReporter(ctx context.Context, state delivery.QueueReporter) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM bindings b JOIN sessions s ON s.harness=b.harness AND s.session_id=b.session_id WHERE b.server=? AND b.member_id=? AND b.generation=? AND b.harness=? AND b.session_id=? AND s.boot=?`, state.Agent.Server, state.Agent.MemberID, state.Generation, state.Session.Harness, state.Session.ID, state.Boot).Scan(&current)
	if err != nil {
		return err
	}
	if current != 1 {
		return fmt.Errorf("queue reporter has no current binding")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO queue_reporters(server,member_id,harness,session_id,boot,generation,state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(server,member_id) DO UPDATE SET harness=excluded.harness,session_id=excluded.session_id,boot=excluded.boot,generation=excluded.generation,state=excluded.state`, state.Agent.Server, state.Agent.MemberID, state.Session.Harness, state.Session.ID, state.Boot, state.Generation, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}
