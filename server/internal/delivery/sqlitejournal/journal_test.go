package sqlitejournal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

func open(t *testing.T, path string) *Journal {
	t.Helper()
	j, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func TestJournalContract(t *testing.T) {
	deliverytest.RunJournal(t, func(t *testing.T) delivery.Journal {
		return open(t, filepath.Join(t.TempDir(), "delivery.db"))
	})
}

func TestJournalFileIsPrivateAndSurvivesReopening(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "delivery.db")
	j := open(t, path)
	agent := delivery.AgentRef{Server: "http://127.0.0.1:7400", Board: "docs", Name: "reviewer"}
	session := delivery.SessionKey{Harness: "codex", ID: "019a"}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := j.AddDelivery(ctx, delivery.Delivery{Agent: agent, Session: session, State: delivery.StateConfirmed, Seqs: []int{6, 7}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("journal mode %o, want 600", perm)
	}
	again := open(t, path)
	got, err := again.Deliveries(ctx, delivery.StateConfirmed)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Seqs) != 2 {
		t.Fatalf("after reopening: %+v", got)
	}
}

// Delivery modes can be read from the file while the daemon has it open, and a journal
// that doesn't exist yet has none.
func TestModesCanBeReadWithoutOpeningTheJournal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "delivery.db")
	got, err := ReadModes(ctx, path)
	if err != nil || len(got) != 0 {
		t.Fatalf("modes of a missing journal: %v %v", got, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reading modes created the journal")
	}
	j := open(t, path)
	agent := delivery.AgentRef{Server: "http://127.0.0.1:7400", Board: "docs", Name: "reviewer"}
	if err := j.SetMode(ctx, agent, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	got, err = ReadModes(ctx, path)
	if err != nil || got[agent] != delivery.ModeHumans {
		t.Fatalf("modes while the journal is open: %v %v", got, err)
	}
}

// A journal written by a newer aboard, with migrations this one doesn't know, is refused
// rather than misread.
func TestJournalFromANewerAboardIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "delivery.db")
	j, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.db.ExecContext(ctx, "PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("opening a newer journal: got %v, want ErrNewerSchema", err)
	}
}

// A journal from before a session held one binding keeps each session's most recent one.
func TestMigrationKeepsEachSessionsLatestBinding(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "delivery.db")
	j, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Go back to the schema that allowed several bindings per session.
	for _, q := range []string{
		"ALTER TABLE sessions DROP COLUMN lost_server",
		"ALTER TABLE sessions DROP COLUMN lost_board",
		"ALTER TABLE sessions DROP COLUMN lost_agent",
		"DROP INDEX bindings_one_per_session",
		"PRAGMA user_version = 3",
		`INSERT INTO bindings VALUES
			('http://127.0.0.1:7400', 'docs', 'reviewer', 'claude-code', 's-a', '2026-10-01T12:00:00Z'),
			('http://127.0.0.1:7400', 'plans', 'planner', 'claude-code', 's-a', '2026-10-01T12:05:00Z'),
			('http://127.0.0.1:7400', 'docs', 'writer', 'codex', 's-a', '2026-10-01T11:00:00Z')`,
	} {
		if _, err := j.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := open(t, path).Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, b := range got {
		names[b.Session.String()] = b.Agent.Name
	}
	if len(got) != 2 || names["claude-code:s-a"] != "planner" || names["codex:s-a"] != "writer" {
		t.Fatalf("bindings after the migration: %+v", got)
	}
}
