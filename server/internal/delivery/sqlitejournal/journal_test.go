package sqlitejournal

import (
	"context"
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
	session := delivery.SessionKey{Harness: delivery.HarnessCodex, ID: "019a"}
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
