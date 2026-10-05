// Package sqlite keeps Aboard's data in SQLite: each board's append-only event log, the
// read models built from it (boards, members, join codes, messages), humans, browser
// logins, saved responses for idempotent writes and the server's own settings. It implements board's
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
	"os"
	"path/filepath"
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
	db   *sql.DB
	clk  clock.Clock // stamps saved responses and backups
	path string
}

// keptBackups is how many copies of the database, each made before a migration, are
// kept in the backups folder beside it.
const keptBackups = 3

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
	s := &Store{db: db, clk: clk, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// ErrNewerSchema means the data was written by a newer aboard, whose schema this one
// doesn't know; reading it could misread it, so Open refuses.
var ErrNewerSchema = errors.New("written by a newer aboard")

// migrationNumber returns the number a migration's file name starts with, or 0.
func migrationNumber(name string) int {
	n, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
	return n
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
	latest := migrationNumber(names[len(names)-1])
	if version > latest {
		return fmt.Errorf("%w: the database is at schema %d, and this aboard knows up to %d", ErrNewerSchema, version, latest)
	}
	if version > 0 && version < latest {
		if err := s.backup(ctx, version); err != nil {
			return err
		}
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
		if err := s.apply(ctx, n, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

// apply runs one migration in a transaction on a connection of its own with foreign keys
// off, as SQLite asks for a migration that rebuilds a table, and checks every foreign key
// before it commits.
func (s *Store) apply(ctx context.Context, n int, body string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open a connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("turn foreign keys off: %w", err)
	}
	// The connection goes back to the pool afterwards, so its foreign keys are turned on
	// again whatever happens.
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON") }()
	sqlTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := applyIn(ctx, sqlTx, n, body); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func applyIn(ctx context.Context, sqlTx *sql.Tx, n int, body string) error {
	if _, err := sqlTx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	if err := checkForeignKeys(ctx, sqlTx); err != nil {
		return err
	}
	if _, err := sqlTx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

// checkForeignKeys fails if any row refers to a row that doesn't exist.
func checkForeignKeys(ctx context.Context, sqlTx *sql.Tx) error {
	rows, err := sqlTx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }() // rows.Err is checked below
	if rows.Next() {
		return errors.New("the migrated data breaks a foreign key")
	}
	return rows.Err()
}

// backupDir is the folder beside the database that holds its copies made before
// migrations.
func (s *Store) backupDir() string { return filepath.Join(filepath.Dir(s.path), "backups") }

// backup copies the database, at schema version, into the backups folder before a
// migration changes it, and keeps only the newest keptBackups copies. The copy is a
// consistent snapshot made while the database is open (VACUUM INTO), readable only by its
// owner.
func (s *Store) backup(ctx context.Context, version int) error {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup folder: %w", err)
	}
	name := fmt.Sprintf("aboard-%s-schema-%d.db", s.clk.Now().UTC().Format("20060102T150405.000Z"), version)
	path := filepath.Join(dir, name)
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("back up the database before migrating it: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect the backup %s: %w", path, err)
	}
	old, err := filepath.Glob(filepath.Join(dir, "aboard-*-schema-*.db"))
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}
	sort.Strings(old) // the names start with the time they were made
	for len(old) > keptBackups {
		if err := os.Remove(old[0]); err != nil {
			return fmt.Errorf("remove an old backup: %w", err)
		}
		old = old[1:]
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

// Setting returns a server setting, and false if it isn't set.
func (s *Store) Setting(ctx context.Context, key string) (value string, ok bool, err error) {
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

// SetSetting stores a server setting, replacing any earlier value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	err := s.write(ctx, func(t *tx) error {
		return t.exec("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	})
	if err != nil {
		return fmt.Errorf("save setting %s: %w", key, err)
	}
	return nil
}
