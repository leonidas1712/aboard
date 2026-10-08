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
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is an open Aboard database.
type Store struct {
	db     *sql.DB
	writer *sql.DB
	clk    clock.Clock // stamps saved responses and backups
	path   string
}

// Readers reuse a small pool rather than opening a SQLite connection per waking
// stream. The writer has its own slot so waiting readers cannot occupy it.
const readerConnections = 4

// OpenReadOnly opens an existing operator-selected database without creating or migrating it.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro"
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(readerConnections)
	db.SetMaxIdleConns(readerConnections)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, clk: clock.Real{}, path: path}, nil
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
	db.SetMaxOpenConns(readerConnections)
	db.SetMaxIdleConns(readerConnections)
	s := &Store{db: db, clk: clk, path: path}
	// A new connection switches the database to WAL, which SQLite refuses at once,
	// without waiting, while another server starting on the same file holds a lock. A
	// failed migration changes nothing, so it is tried again until the busy timeout.
	err = s.migrate(ctx, migrations)
	for deadline := time.Now().Add(10 * time.Second); busy(err) && time.Now().Before(deadline); err = s.migrate(ctx, migrations) {
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// Migration established WAL. The writer only configures its connection; it does
	// not try to change journal mode while another process may be writing.
	q.Del("_pragma")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	writer, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	s.writer = writer
	return s, nil
}

// busy reports whether err is SQLite's "database is locked" (SQLITE_BUSY).
func busy(err error) bool {
	var e interface{ Code() int }
	return errors.As(err, &e) && e.Code()&0xff == 5
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
func (s *Store) Close() error {
	var writerErr error
	if s.writer != nil {
		writerErr = s.writer.Close()
	}
	return errors.Join(writerErr, s.db.Close())
}

func (s *Store) migrate(ctx context.Context, files fs.FS) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)
	latest := migrationNumber(names[len(names)-1])
	if version > latest {
		return fmt.Errorf("%w: the database is at schema %d, and this aboard knows up to %d", ErrNewerSchema, version, latest)
	}
	var pending []migration
	for _, name := range names {
		n, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number: %w", name, err)
		}
		if n <= version {
			continue
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		pending = append(pending, migration{name: name, n: n, body: string(body)})
	}
	if len(pending) == 0 {
		return nil
	}
	backup := ""
	if version > 0 {
		if backup, err = s.backup(ctx, version); err != nil {
			return err
		}
	}
	if err := s.apply(ctx, pending); err != nil {
		if backup != "" {
			return fmt.Errorf("%w; the database is unchanged, at schema %d, and a copy of it from before is at %s", err, version, backup)
		}
		return err
	}
	return nil
}

// migration is one numbered migration file.
type migration struct {
	name string
	n    int
	body string
}

// apply runs the migrations in one transaction, so a failure leaves the database as it
// was. The transaction has a connection of its own with foreign keys off, as SQLite asks
// for a migration that rebuilds a table, and every foreign key is checked after each
// migration.
func (s *Store) apply(ctx context.Context, pending []migration) error {
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
	// Another server starting on the same database may have migrated it since the version
	// was read; the transaction holds the write lock, so what it reads now stays true.
	var current int
	if err := sqlTx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		_ = sqlTx.Rollback()
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, m := range pending {
		if m.n <= current {
			continue
		}
		if err := applyIn(ctx, sqlTx, m.n, m.body); err != nil {
			_ = sqlTx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit the migrations: %w", err)
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

// privateDir makes dir, readable only by its owner, unless it exists. One that exists
// must be a real folder, not a link, that no one else can open: a copy of the database
// holds everything in it.
func privateDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return fmt.Errorf("create the backup folder %s: %w", dir, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("check the backup folder %s: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is a link or a file, not the backup folder; move it away so the next start can make the folder", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the backup folder %s can be opened by others (mode %04o); run chmod 700 %s", dir, info.Mode().Perm(), dir)
	}
	return nil
}

// backup copies the database, at schema version, into the backups folder before a
// migration changes it, keeps only the newest keptBackups copies and returns the copy's
// path. The copy is a consistent snapshot made while the database is open (VACUUM INTO),
// in a file created readable only by its owner before SQLite writes to it.
func (s *Store) backup(ctx context.Context, version int) (string, error) {
	dir := s.backupDir()
	if err := privateDir(dir); err != nil {
		return "", fmt.Errorf("back up the database before migrating it: %w", err)
	}
	name := fmt.Sprintf("aboard-%s-schema-%d.db", s.clk.Now().UTC().Format("20060102T150405.000Z"), version)
	path := filepath.Join(dir, name)
	// VACUUM INTO writes into an empty file it finds there, keeping the file's mode.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // a name made here, in the database's own folder
	if err != nil {
		return "", fmt.Errorf("back up the database before migrating it: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("back up the database before migrating it: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("back up the database before migrating it: %w", err)
	}
	old, err := filepath.Glob(filepath.Join(dir, "aboard-*-schema-*.db"))
	if err != nil {
		return "", fmt.Errorf("list backups: %w", err)
	}
	sort.Strings(old) // the names start with the time they were made
	for len(old) > keptBackups {
		if err := os.Remove(old[0]); err != nil {
			return "", fmt.Errorf("remove an old backup: %w", err)
		}
		old = old[1:]
	}
	return path, nil
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
	db := s.writer
	if db == nil {
		db = s.db
	}
	sqlTx, err := db.BeginTx(ctx, nil)
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

// SettingOnce stores a server setting unless it has a value already, and returns the
// value it has afterwards: value, or the one stored first. Stored values never change,
// so two servers starting at once on one database agree on the one that was kept.
func (s *Store) SettingOnce(ctx context.Context, key, value string) (string, error) {
	var stored string
	err := s.write(ctx, func(t *tx) error {
		if err := t.exec("INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING", key, value); err != nil {
			return err
		}
		return t.queryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&stored)
	})
	if err != nil {
		return "", fmt.Errorf("save setting %s: %w", key, err)
	}
	return stored, nil
}
