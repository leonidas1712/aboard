package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/board/boardtest"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

func TestSQLiteIsABoardStore(t *testing.T) {
	boardtest.Run(t, func(t *testing.T) board.Store {
		clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
		st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "aboard.db"), clk)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	})
}

// A database written by a newer aboard, with migrations this one doesn't know, is
// refused rather than misread.
func TestDatabaseFromANewerAboardIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := sqlite.Open(ctx, path, clock.Real{}); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Fatalf("opening a newer database: got %v, want ErrNewerSchema", err)
	}
}

// People on boards created before access levels existed get them on upgrade: each
// board's creator is its admin and every other person a member; agents get none.
func TestUpgradeGivesExistingPeopleAccessLevels(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	initial, err := os.ReadFile(filepath.Join("migrations", "0001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		string(initial),
		"PRAGMA user_version = 1",
		`INSERT INTO humans VALUES ('hum_alex', 'alex', 'd1', 'x'), ('hum_sam', 'sam', 'd2', 'x')`,
		`INSERT INTO boards VALUES ('brd_a', 'docs', NULL, '', '{}', '{}', 0, 'h', 'x', 'mem_alex')`,
		`INSERT INTO members (id, board_id, name, kind, role, human_id, owner, status, joined_at) VALUES
			('mem_alex', 'brd_a', 'alex', 'human', NULL, 'hum_alex', NULL, 'active', 'x'),
			('mem_sam', 'brd_a', 'sam', 'human', NULL, 'hum_sam', NULL, 'active', 'x'),
			('mem_writer', 'brd_a', 'writer', 'agent', 'writer', 'hum_sam', 'sam', 'active', 'x')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	got := map[string]string{}
	err = st.Read(ctx, func(tx board.ReadTx) error {
		ms, err := tx.Members("brd_a")
		for _, m := range ms {
			got[m.Name] = m.Access
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"alex": "admin", "sam": "member", "writer": ""}; !maps.Equal(got, want) {
		t.Fatalf("access after upgrade %v, want %v", got, want)
	}
}
