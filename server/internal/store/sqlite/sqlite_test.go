package sqlite_test

import (
	"context"
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
