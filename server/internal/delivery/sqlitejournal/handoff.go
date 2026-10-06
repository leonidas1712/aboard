package sqlitejournal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func bindingGeneration(ctx context.Context, tx *sql.Tx, b delivery.Binding, advance bool) (uint64, error) {
	var generation uint64
	err := tx.QueryRowContext(ctx, `SELECT generation FROM seat_generations WHERE server = ? AND member_id = ?`, b.Agent.Server, b.Agent.MemberID).Scan(&generation)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		generation = 1
	case err != nil:
		return 0, err
	default:
		var session delivery.SessionKey
		err = tx.QueryRowContext(ctx, `SELECT harness, session_id FROM bindings WHERE server = ? AND member_id = ?`, b.Agent.Server, b.Agent.MemberID).Scan(&session.Harness, &session.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if advance || errors.Is(err, sql.ErrNoRows) || session != b.Session {
			generation++
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO seat_generations (server,member_id,generation) VALUES (?,?,?) ON CONFLICT (server,member_id) DO UPDATE SET generation = excluded.generation`, b.Agent.Server, b.Agent.MemberID, generation)
	return generation, err
}

func validateManifest(m delivery.HandoffManifest) error {
	id := strings.TrimPrefix(m.ID, "hnd_")
	if !strings.HasPrefix(m.ID, "hnd_") || len(id) != 32 {
		return errors.New("invalid handoff id")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("invalid handoff id")
	}
	if len(m.PayloadHash) != 64 {
		return errors.New("invalid handoff payload hash")
	}
	if _, err := hex.DecodeString(m.PayloadHash); err != nil {
		return errors.New("invalid handoff payload hash")
	}
	if m.Class != delivery.ClassMixed && m.Class != delivery.ClassOwnerOnly {
		return errors.New("invalid handoff class")
	}
	if m.Session.Harness == "" || m.Session.ID == "" || m.Boot == "" || len(m.Parts) == 0 || m.CreatedAt.IsZero() {
		return errors.New("incomplete handoff manifest")
	}
	seen := map[delivery.AgentKey]bool{}
	rows := map[int64]bool{}
	for _, p := range m.Parts {
		if p.Agent.MemberID == "" || p.Agent.Server == "" || p.Agent.Board == "" || p.Generation == 0 || len(p.Seqs) == 0 || p.DeliveryID < 0 || seen[p.Agent.Key()] {
			return errors.New("invalid handoff part")
		}
		if p.DeliveryID > 0 && rows[p.DeliveryID] {
			return errors.New("duplicate handoff delivery")
		}
		rows[p.DeliveryID] = true
		seen[p.Agent.Key()] = true
		for i, seq := range p.Seqs {
			if seq <= 0 || (i > 0 && seq <= p.Seqs[i-1]) {
				return errors.New("handoff sequences must be positive and increasing")
			}
		}
	}
	return nil
}

func loadHandoff(ctx context.Context, tx *sql.Tx, id string) (delivery.HandoffManifest, error) {
	var m delivery.HandoffManifest
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT manifest FROM handoffs WHERE id = ?", id).Scan(&raw); err != nil {
		return m, err
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return m, fmt.Errorf("decode handoff manifest: %w", err)
	}
	return m, nil
}

func currentPart(ctx context.Context, tx *sql.Tx, m delivery.HandoffManifest, p delivery.HandoffPart) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bindings b JOIN sessions s ON s.harness = b.harness AND s.session_id = b.session_id
 WHERE b.server = ? AND b.member_id = ? AND b.board = ? AND b.generation = ? AND b.harness = ? AND b.session_id = ? AND s.boot = ? AND s.open = 1`, p.Agent.Server, p.Agent.MemberID, p.Agent.Board, p.Generation, m.Session.Harness, m.Session.ID, m.Boot).Scan(&count)
	return count == 1, err
}

// PrepareHandoff commits the immutable manifest and every handed delivery together.
func (j *Journal) PrepareHandoff(ctx context.Context, m delivery.HandoffManifest) (delivery.HandoffManifest, error) {
	if err := validateManifest(m); err != nil {
		return delivery.HandoffManifest{}, err
	}
	var saved delivery.HandoffManifest
	err := j.write(ctx, func(tx *sql.Tx) error {
		previous, err := loadHandoff(ctx, tx, m.ID)
		if err == nil {
			compare := m
			compare.Parts = append([]delivery.HandoffPart(nil), m.Parts...)
			if len(compare.Parts) != len(previous.Parts) {
				return errors.New("handoff manifest changed")
			}
			for i := range compare.Parts {
				if compare.Parts[i].DeliveryID == 0 {
					compare.Parts[i].DeliveryID = previous.Parts[i].DeliveryID
				}
			}
			left, err := json.Marshal(compare)
			if err != nil {
				return err
			}
			right, err := json.Marshal(previous)
			if err != nil {
				return err
			}
			if !bytes.Equal(left, right) {
				return errors.New("handoff manifest changed")
			}
			for _, p := range previous.Parts {
				current, err := currentPart(ctx, tx, previous, p)
				if err != nil {
					return err
				}
				if !current {
					return errors.New("handoff binding or boot changed")
				}
			}
			for _, p := range previous.Parts {
				var active, state string
				if err := tx.QueryRowContext(ctx, `SELECT handoff_id, state FROM deliveries WHERE id = ?`, p.DeliveryID).Scan(&active, &state); err != nil {
					return err
				}
				if active != previous.ID || (state != string(delivery.StateHanded) && state != string(delivery.StatePending) && state != string(delivery.StateRetry) && state != string(delivery.StateHeld)) {
					return errors.New("handoff delivery was superseded or ended")
				}
				if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET state = ? WHERE id = ?`, string(delivery.StateHanded), p.DeliveryID); err != nil {
					return err
				}
			}
			saved = previous
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		m.Parts = append([]delivery.HandoffPart(nil), m.Parts...)
		for i, p := range m.Parts {
			current, err := currentPart(ctx, tx, m, p)
			if err != nil {
				return err
			}
			if !current {
				return errors.New("handoff binding or boot changed")
			}
			if p.DeliveryID > 0 {
				if err := reuseDelivery(ctx, tx, m, p); err != nil {
					return err
				}
			} else {
				res, err := tx.ExecContext(ctx, `INSERT INTO deliveries (server,board,agent,member_id,harness,session_id,boot,state,created_at,updated_at,handoff_id) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, p.Agent.Server, p.Agent.Board, p.Agent.Name, p.Agent.MemberID, m.Session.Harness, m.Session.ID, m.Boot, string(delivery.StateHanded), formatTime(m.CreatedAt), formatTime(m.CreatedAt), m.ID)
				if err != nil {
					return err
				}
				id, err := res.LastInsertId()
				if err != nil {
					return err
				}
				m.Parts[i].DeliveryID = id
				for _, seq := range p.Seqs {
					if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_messages (delivery_id,seq) VALUES (?,?)`, id, seq); err != nil {
						return err
					}
				}
			}
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO handoffs (id,manifest) VALUES (?,?)`, m.ID, string(raw)); err != nil {
			return err
		}
		saved = m
		return nil
	})
	if err != nil {
		return delivery.HandoffManifest{}, fmt.Errorf("prepare handoff: %w", err)
	}
	return saved, nil
}

func reuseDelivery(ctx context.Context, tx *sql.Tx, m delivery.HandoffManifest, p delivery.HandoffPart) error {
	var a delivery.AgentRef
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT server,board,agent,member_id,state FROM deliveries WHERE id = ?`, p.DeliveryID).Scan(&a.Server, &a.Board, &a.Name, &a.MemberID, &state); err != nil {
		return err
	}
	if a.Key() != p.Agent.Key() || a.Board != p.Agent.Board {
		return errors.New("handoff delivery belongs to another seat")
	}
	if state != string(delivery.StatePending) && state != string(delivery.StateRetry) && state != string(delivery.StateHeld) {
		return errors.New("handoff delivery is not retryable")
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq FROM delivery_messages WHERE delivery_id = ? ORDER BY seq`, p.DeliveryID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var seqs []int
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			return err
		}
		seqs = append(seqs, seq)
	}
	err = rows.Err()
	if err != nil {
		return err
	}
	if !slices.Equal(seqs, p.Seqs) {
		return errors.New("handoff delivery sequences changed")
	}
	_, err = tx.ExecContext(ctx, `UPDATE deliveries SET state = ?, harness = ?, session_id = ?, agent = ?, boot = ?, updated_at = ?, accepted_at = '', turn_started_at = '', stalled = 0, handoff_id = ? WHERE id = ?`, string(delivery.StateHanded), m.Session.Harness, m.Session.ID, p.Agent.Name, m.Boot, formatTime(m.CreatedAt), m.ID, p.DeliveryID)
	return err
}

// ConfirmHandoff persists evidence for the current, nonterminal whitelist only.
func (j *Journal) ConfirmHandoff(ctx context.Context, id string, session delivery.SessionKey, boot string, surviving []delivery.AgentKey, at time.Time) ([]delivery.Delivery, error) {
	var confirmed []delivery.Delivery
	err := j.write(ctx, func(tx *sql.Tx) error {
		m, err := loadHandoff(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if m.Session != session || m.Boot != boot {
			return nil
		}
		for _, p := range m.Parts {
			if !slices.Contains(surviving, p.Agent.Key()) {
				continue
			}
			current, err := currentPart(ctx, tx, m, p)
			if err != nil {
				return err
			}
			if !current {
				continue
			}
			var state, active string
			if err := tx.QueryRowContext(ctx, `SELECT state, handoff_id FROM deliveries WHERE id = ?`, p.DeliveryID).Scan(&state, &active); err != nil {
				return err
			}
			if state != string(delivery.StateHanded) || active != m.ID {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET state = ?, accepted_at = ?, updated_at = ? WHERE id = ?`, string(delivery.StateConfirmed), formatTime(at), formatTime(at), p.DeliveryID); err != nil {
				return err
			}
			d, err := readHandoffDelivery(ctx, tx, p.DeliveryID)
			if err != nil {
				return err
			}
			confirmed = append(confirmed, d)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("confirm handoff: %w", err)
	}
	return confirmed, nil
}

// Handoffs returns frozen manifests in preparation order for recovery.
func (j *Journal) Handoffs(ctx context.Context) ([]delivery.HandoffManifest, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT manifest FROM handoffs ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.HandoffManifest
	for rows.Next() {
		var raw string
		var m delivery.HandoffManifest
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func readHandoffDelivery(ctx context.Context, tx *sql.Tx, id int64) (delivery.Delivery, error) {
	var d delivery.Delivery
	var state, retry, created, updated, accepted, started string
	err := tx.QueryRowContext(ctx, `SELECT id, server, board, agent, member_id, harness, session_id, boot, state, attempts, reason, retry_at, created_at, updated_at, accepted_at, turn_started_at, stalled, handoff_id FROM deliveries WHERE id = ?`, id).Scan(&d.ID, &d.Agent.Server, &d.Agent.Board, &d.Agent.Name, &d.Agent.MemberID, &d.Session.Harness, &d.Session.ID, &d.Boot, &state, &d.Attempts, &d.Reason, &retry, &created, &updated, &accepted, &started, &d.Stalled, &d.HandoffID)
	if err != nil {
		return d, err
	}
	d.State = delivery.State(state)
	for _, field := range []struct {
		raw    string
		target *time.Time
	}{{retry, &d.RetryAt}, {created, &d.CreatedAt}, {updated, &d.UpdatedAt}, {accepted, &d.AcceptedAt}, {started, &d.TurnStartedAt}} {
		*field.target, err = parseTime(field.raw)
		if err != nil {
			return d, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq FROM delivery_messages WHERE delivery_id = ? ORDER BY seq`, id)
	if err != nil {
		return d, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			return d, err
		}
		d.Seqs = append(d.Seqs, seq)
	}
	return d, rows.Err()
}
