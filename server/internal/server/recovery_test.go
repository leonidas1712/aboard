package server

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// When checking a pending key fails for a reason other than the key not working, such
// as a cancelled start or a database error, the pending key stays for the next start
// and the error is returned.
func TestACheckThatFailsKeepsThePendingKey(t *testing.T) {
	data := dataDir(t)
	undo := stopAt(t, "admin created")
	if _, err := launchTeam(t, data); err == nil {
		t.Fatal("the start went on")
	}
	undo()
	svc, st := openService(t, data)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := finishBootstrap(ctx, svc, data, quiet); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("finishing with a cancelled context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, pendingKeyFile)); err != nil {
		t.Fatalf("the pending key was removed after a cancelled check: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := finishBootstrap(context.Background(), svc, data, quiet); err == nil {
		t.Fatal("finishing with the database closed succeeded")
	}
	if _, err := os.Stat(filepath.Join(data, pendingKeyFile)); err != nil {
		t.Fatalf("the pending key was removed after a database error: %v", err)
	}
	s := startTeam(t, data)
	adminWorks(t, s, s.adminKey())
	onlyTheKeyFile(t, data)
}

// openService opens the team server's database in data as a service, as Run does.
func openService(t *testing.T, data string) (*board.Service, *sqlite.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(data, "aboard.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, key, err := identity(ctx, st, Options{Clock: clock.Real{}, Rand: rand.Reader})
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return board.New(st, notify.NewInProcess(), clock.Real{}, ids.New(rand.Reader), key, board.Config{ServerID: id, Mode: "team"}, quiet), st
}
