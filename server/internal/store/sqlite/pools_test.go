package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestAWriterCanCommitWhileAllReaderConnectionsAreOccupied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	st.db.SetMaxOpenConns(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- st.Read(ctx, func(tx board.ReadTx) error {
			_, err := tx.BoardByName("missing")
			if !errors.Is(err, board.ErrNotFound) {
				return err
			}
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	t.Cleanup(func() { close(release); <-readerDone })
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if value, err := st.SettingOnce(ctx, "writer-is-not-a-reader", "committed"); err != nil || value != "committed" {
		t.Fatalf("writer could not commit while readers are occupied: value %q, error %v", value, err)
	}
}
