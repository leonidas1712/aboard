// Package sqlitejournal keeps the delivery daemon's journal in its own SQLite file:
// sessions, bindings and every delivery's state, with the sequence numbers of the
// messages in it. It implements the delivery package's Journal port and never stores
// tokens or message bodies.
package sqlitejournal

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Journal is an open delivery journal.
type Journal struct {
	db *sql.DB
}

var _ delivery.Journal = (*Journal)(nil)

// Open opens (creating if needed) the journal at path, readable only by its owner, and
// applies migrations.
func Open(ctx context.Context, path string) (*Journal, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("set permissions on %s: %w", path, err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	j := &Journal{db: db}
	if err := j.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return j, nil
}

// ErrNewerSchema means the data was written by a newer aboard, whose schema this one
// doesn't know; reading it could misread it, so Open refuses.
var ErrNewerSchema = errors.New("written by a newer aboard")

// migrationNumber returns the number a migration's file name starts with, or 0.
func migrationNumber(name string) int {
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
	return n
}

// Close closes the journal.
func (j *Journal) Close() error { return j.db.Close() }

func (j *Journal) migrate(ctx context.Context) error {
	var version int
	if err := j.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read journal schema version: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	if latest := migrationNumber(names[len(names)-1]); version > latest {
		return fmt.Errorf("%w: the journal is at schema %d, and this aboard knows up to %d", ErrNewerSchema, version, latest)
	}
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number: %w", name, err)
		}
		if n <= version {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		err = j.write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("apply: %w", err)
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n))
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (j *Journal) write(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

const timeFormat = time.RFC3339Nano

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	return t, nil
}

// SaveSession records a session's boot id, its harness process and whether it is open.
func (j *Journal) SaveSession(ctx context.Context, s delivery.SessionRecord) error {
	var p delivery.Process
	if s.Process != nil {
		p = *s.Process
	}
	return j.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO sessions (harness, session_id, boot, open, pid, pid_start, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (harness, session_id) DO UPDATE SET boot = excluded.boot, open = excluded.open,
				pid = excluded.pid, pid_start = excluded.pid_start, updated_at = excluded.updated_at`,
			s.Key.Harness, s.Key.ID, s.Boot, s.Open, p.PID, p.Start, formatTime(s.UpdatedAt))
		if err != nil {
			return fmt.Errorf("save session %s: %w", s.Key, err)
		}
		return nil
	})
}

// Sessions returns every recorded session.
func (j *Journal) Sessions(ctx context.Context) ([]delivery.SessionRecord, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT harness, session_id, boot, open, pid, pid_start, updated_at FROM sessions ORDER BY harness, session_id`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.SessionRecord
	for rows.Next() {
		var s delivery.SessionRecord
		var p delivery.Process
		var updated string
		if err := rows.Scan(&s.Key.Harness, &s.Key.ID, &s.Boot, &s.Open, &p.PID, &p.Start, &updated); err != nil {
			return nil, fmt.Errorf("read session: %w", err)
		}
		if p.PID != 0 {
			s.Process = &p
		}
		if s.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return out, nil
}

// Bind records the session an agent's messages go to, replacing any earlier one.
func (j *Journal) Bind(ctx context.Context, b delivery.Binding) error {
	return j.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO bindings (server, board, agent, harness, session_id, bound_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (server, board, agent) DO UPDATE SET harness = excluded.harness, session_id = excluded.session_id, bound_at = excluded.bound_at`,
			b.Agent.Server, b.Agent.Board, b.Agent.Name, b.Session.Harness, b.Session.ID, formatTime(b.BoundAt))
		if err != nil {
			return fmt.Errorf("bind %s on %s: %w", b.Agent.Name, b.Agent.Board, err)
		}
		return nil
	})
}

// Bindings returns every agent's binding.
func (j *Journal) Bindings(ctx context.Context) ([]delivery.Binding, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT server, board, agent, harness, session_id, bound_at FROM bindings ORDER BY server, board, agent`)
	if err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.Binding
	for rows.Next() {
		var b delivery.Binding
		var bound string
		if err := rows.Scan(&b.Agent.Server, &b.Agent.Board, &b.Agent.Name, &b.Session.Harness, &b.Session.ID, &bound); err != nil {
			return nil, fmt.Errorf("read binding: %w", err)
		}
		if b.BoundAt, err = parseTime(bound); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list bindings: %w", err)
	}
	return out, nil
}

// SetMode records an agent's delivery mode, replacing any earlier one.
func (j *Journal) SetMode(ctx context.Context, agent delivery.AgentRef, mode delivery.Mode) error {
	return j.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO modes (server, board, agent, mode) VALUES (?, ?, ?, ?)
			ON CONFLICT (server, board, agent) DO UPDATE SET mode = excluded.mode`,
			agent.Server, agent.Board, agent.Name, string(mode))
		if err != nil {
			return fmt.Errorf("set delivery mode of %s on %s: %w", agent.Name, agent.Board, err)
		}
		return nil
	})
}

// Modes returns every agent's recorded delivery mode.
func (j *Journal) Modes(ctx context.Context) (map[delivery.AgentRef]delivery.Mode, error) {
	return readModes(ctx, j.db)
}

func readModes(ctx context.Context, db *sql.DB) (map[delivery.AgentRef]delivery.Mode, error) {
	rows, err := db.QueryContext(ctx, `SELECT server, board, agent, mode FROM modes`)
	if err != nil {
		return nil, fmt.Errorf("list delivery modes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[delivery.AgentRef]delivery.Mode{}
	for rows.Next() {
		var a delivery.AgentRef
		var mode string
		if err := rows.Scan(&a.Server, &a.Board, &a.Name, &mode); err != nil {
			return nil, fmt.Errorf("read delivery mode: %w", err)
		}
		out[a] = delivery.Mode(mode)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list delivery modes: %w", err)
	}
	return out, nil
}

// ReadModes returns the delivery modes recorded in the journal at path without opening
// it for writing or changing its schema, so it can be read while a daemon, perhaps an
// older one, has it open. A journal that doesn't exist, or predates modes, has none.
func ReadModes(ctx context.Context, path string) (map[delivery.AgentRef]delivery.Mode, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return map[delivery.AgentRef]delivery.Mode{}, nil
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("mode", "ro")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, fmt.Errorf("read journal schema version: %w", err)
	}
	if version < modesVersion {
		return map[delivery.AgentRef]delivery.Mode{}, nil
	}
	return readModes(ctx, db)
}

// modesVersion is the journal schema version that added delivery modes.
const modesVersion = 3

// AddDelivery records a delivery and its messages in one transaction.
func (j *Journal) AddDelivery(ctx context.Context, d delivery.Delivery) (int64, error) {
	if len(d.Seqs) == 0 {
		return 0, errors.New("a delivery needs at least one message")
	}
	var id int64
	err := j.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO deliveries (server, board, agent, harness, session_id, boot, state, attempts, reason, retry_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.Agent.Server, d.Agent.Board, d.Agent.Name, d.Session.Harness, d.Session.ID, d.Boot, string(d.State),
			d.Attempts, d.Reason, formatTime(d.RetryAt), formatTime(d.CreatedAt), formatTime(d.UpdatedAt))
		if err != nil {
			return fmt.Errorf("add delivery: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("add delivery: %w", err)
		}
		for _, seq := range d.Seqs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_messages (delivery_id, seq) VALUES (?, ?)`, id, seq); err != nil {
				return fmt.Errorf("add message %d to delivery %d: %w", seq, id, err)
			}
		}
		return nil
	})
	return id, err
}

// ErrNoDelivery means there is no delivery with the given id.
var ErrNoDelivery = errors.New("no such delivery")

// UpdateDelivery saves a delivery's state, session, attempts, reason and retry time.
func (j *Journal) UpdateDelivery(ctx context.Context, d delivery.Delivery) error {
	return j.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE deliveries SET harness = ?, session_id = ?, boot = ?, state = ?, attempts = ?, reason = ?, retry_at = ?, updated_at = ?
			WHERE id = ?`,
			d.Session.Harness, d.Session.ID, d.Boot, string(d.State), d.Attempts, d.Reason,
			formatTime(d.RetryAt), formatTime(d.UpdatedAt), d.ID)
		if err != nil {
			return fmt.Errorf("update delivery %d: %w", d.ID, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("update delivery %d: %w", d.ID, err)
		} else if n == 0 {
			return fmt.Errorf("update delivery %d: %w", d.ID, ErrNoDelivery)
		}
		return nil
	})
}

// Deliveries returns the deliveries in the given states, oldest first, with their
// messages' sequence numbers in order.
func (j *Journal) Deliveries(ctx context.Context, states ...delivery.State) ([]delivery.Delivery, error) {
	var out []delivery.Delivery
	for _, st := range states {
		ds, err := j.deliveriesIn(ctx, st)
		if err != nil {
			return nil, err
		}
		out = append(out, ds...)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

func (j *Journal) deliveriesIn(ctx context.Context, state delivery.State) ([]delivery.Delivery, error) {
	rows, err := j.db.QueryContext(ctx, `
		SELECT d.id, d.server, d.board, d.agent, d.harness, d.session_id, d.boot, d.attempts, d.reason,
		       d.retry_at, d.created_at, d.updated_at, m.seq
		FROM deliveries d JOIN delivery_messages m ON m.delivery_id = d.id
		WHERE d.state = ?
		ORDER BY d.id, m.seq`, string(state))
	if err != nil {
		return nil, fmt.Errorf("list %s deliveries: %w", state, err)
	}
	defer func() { _ = rows.Close() }()
	var out []delivery.Delivery
	for rows.Next() {
		d := delivery.Delivery{State: state}
		var retry, created, updated string
		var seq int
		if err := rows.Scan(&d.ID, &d.Agent.Server, &d.Agent.Board, &d.Agent.Name, &d.Session.Harness, &d.Session.ID,
			&d.Boot, &d.Attempts, &d.Reason, &retry, &created, &updated, &seq); err != nil {
			return nil, fmt.Errorf("read delivery: %w", err)
		}
		if n := len(out); n > 0 && out[n-1].ID == d.ID {
			out[n-1].Seqs = append(out[n-1].Seqs, seq)
			continue
		}
		d.Seqs = []int{seq}
		if d.RetryAt, err = parseTime(retry); err != nil {
			return nil, err
		}
		if d.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if d.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s deliveries: %w", state, err)
	}
	return out, nil
}

// RecoverHanded puts handed deliveries back to pending after a restart.
func (j *Journal) RecoverHanded(ctx context.Context, now time.Time) (int, error) {
	var n int64
	err := j.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE deliveries SET state = ?, updated_at = ? WHERE state = ?`,
			string(delivery.StatePending), formatTime(now), string(delivery.StateHanded))
		if err != nil {
			return fmt.Errorf("recover handed deliveries: %w", err)
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}
