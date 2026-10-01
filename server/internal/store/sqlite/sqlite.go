// Package sqlite keeps Aboard's data in SQLite: each board's append-only event log, the
// read models built from it (boards, members, join codes, messages), humans, saved
// responses for idempotent writes and the server's own settings. It implements board's
// Store port and api's Responses port. Writes hold the write lock from the start, so
// event sequence numbers never race.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is an open Aboard database.
type Store struct {
	db  *sql.DB
	clk clock.Clock // stamps saved responses
}

var _ board.Store = (*Store)(nil)

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string, clk clock.Clock) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	// BEGIN IMMEDIATE takes the write lock when a transaction starts, so two writers
	// can't both read a board's head and then append the same seq.
	q.Add("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db, clk: clk}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
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
		err = s.write(ctx, func(t *tx) error {
			if err := t.exec(string(body)); err != nil {
				return fmt.Errorf("apply: %w", err)
			}
			return t.exec(fmt.Sprintf("PRAGMA user_version = %d", n))
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// tx is one transaction. Its methods read and write within it.
type tx struct {
	tx  *sql.Tx
	ctx context.Context
}

var _ board.Tx = (*tx)(nil)

// Write runs fn in a transaction, committing if it returns nil and rolling back otherwise.
func (s *Store) Write(ctx context.Context, fn func(board.Tx) error) error {
	return s.write(ctx, func(t *tx) error { return fn(t) })
}

func (s *Store) write(ctx context.Context, fn func(*tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(&tx{tx: sqlTx, ctx: ctx}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Read runs fn in a transaction that is always rolled back, so it sees one consistent
// state of the database.
func (s *Store) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	return s.read(ctx, func(t *tx) error { return fn(t) })
}

func (s *Store) read(ctx context.Context, fn func(*tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(&tx{tx: sqlTx, ctx: ctx})
}

func (t *tx) exec(query string, args ...any) error {
	_, err := t.tx.ExecContext(t.ctx, query, args...)
	return err
}

func (t *tx) queryRow(query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(t.ctx, query, args...)
}

// notFound turns the driver's "no rows" into the domain's ErrNotFound, so callers never
// see a database/sql error.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return board.ErrNotFound
	}
	return err
}

// Meta returns a server setting, and false if it isn't set.
func (s *Store) Meta(ctx context.Context, key string) (value string, ok bool, err error) {
	var v string
	err = s.read(ctx, func(t *tx) error {
		return t.queryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return v, true, nil
}

// SetMeta stores a server setting, replacing any earlier value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	err := s.write(ctx, func(t *tx) error {
		return t.exec("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	})
	if err != nil {
		return fmt.Errorf("save setting %s: %w", key, err)
	}
	return nil
}
