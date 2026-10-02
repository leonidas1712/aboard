package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
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
