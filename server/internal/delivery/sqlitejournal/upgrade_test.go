package sqlitejournal

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Build the historical schema directly: downgrading the current schema would leave
// future columns and indexes in the fixture and could hide a broken upgrade.
func historicalJournal(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "delivery.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	files := []string{
		"001_journal.sql", "002_session_process.sql", "003_modes.sql",
		"004_one_binding_per_session.sql", "005_session_lost.sql", "006_delivery_stages.sql", "007_session_turned.sql",
	}
	for _, name := range files[:version] {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpgradePreservesLegacySessionAndUnacknowledgedDeliveries(t *testing.T) {
	ctx := context.Background()
	path := historicalJournal(t, 7)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO sessions (harness, session_id, boot, open, updated_at, pid, pid_start, lost_server, lost_board, lost_agent, turned)
		 VALUES ('codex', 's-old', 'boot-old', 1, '2026-10-01T12:00:00Z', 42, 123, 'http://127.0.0.1:7400', 'other', 'writer', 1)`,
		`INSERT INTO bindings VALUES ('http://127.0.0.1:7400', 'docs', 'reviewer', 'codex', 's-old', '2026-10-01T12:00:00Z')`,
		`INSERT INTO modes VALUES ('http://127.0.0.1:7400', 'docs', 'reviewer', 'humans')`,
		`INSERT INTO deliveries (id, server, board, agent, harness, session_id, boot, state, attempts, reason, retry_at, created_at, updated_at, accepted_at, turn_started_at, stalled)
		 VALUES (17, 'http://127.0.0.1:7400', 'docs', 'reviewer', 'codex', 's-old', 'boot-old', 'confirmed', 2, '', '', '2026-10-01T12:00:00Z', '2026-10-01T12:00:00Z', '2026-10-01T12:00:00Z', '', 0)`,
		`INSERT INTO delivery_messages VALUES (17, 6), (17, 7)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	j := open(t, path)
	ref := delivery.AgentRef{Server: "http://127.0.0.1:7400", Board: "docs", Name: "reviewer"}
	sessions, err := j.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Boot != "boot-old" || !sessions[0].Open || !sessions[0].Turned ||
		sessions[0].Process == nil || *sessions[0].Process != (delivery.Process{PID: 42, Start: 123}) ||
		sessions[0].Lost == nil || sessions[0].Lost.Name != "writer" {
		t.Fatalf("legacy session: %+v, %v", sessions, err)
	}
	bindings, err := j.Bindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Agent != ref || bindings[0].Session != sessions[0].Key {
		t.Fatalf("legacy bindings: %+v, %v", bindings, err)
	}
	modes, err := j.Modes(ctx)
	if err != nil || modes[ref] != delivery.ModeHumans {
		t.Fatalf("legacy modes: %+v, %v", modes, err)
	}
	deliveries, err := j.Deliveries(ctx, delivery.StateConfirmed)
	if err != nil || len(deliveries) != 1 || deliveries[0].ID != 17 || deliveries[0].Agent != ref ||
		!slices.Equal(deliveries[0].Seqs, []int{6, 7}) || deliveries[0].Attempts != 2 || deliveries[0].AcceptedAt.IsZero() {
		t.Fatalf("legacy deliveries awaiting ack: %+v, %v", deliveries, err)
	}
	resolved := ref
	resolved.MemberID = "mem_reviewer"
	if err := j.ResolveIdentity(ctx, ref, resolved); err != nil {
		t.Fatal(err)
	}
	lost := *sessions[0].Lost
	resolvedLost := lost
	resolvedLost.MemberID = "mem_writer"
	if err := j.ResolveIdentity(ctx, lost, resolvedLost); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = open(t, path)
	bindings, err = j.Bindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Agent != resolved {
		t.Fatalf("reopened resolved binding: %+v, %v", bindings, err)
	}
	modes, err = ReadModes(ctx, path)
	if err != nil || len(modes) != 1 || modes[resolved] != delivery.ModeHumans {
		t.Fatalf("reopened resolved mode: %+v, %v", modes, err)
	}
	deliveries, err = j.Deliveries(ctx, delivery.StateConfirmed)
	if err != nil || len(deliveries) != 1 || deliveries[0].Agent != resolved || deliveries[0].ID != 17 || !slices.Equal(deliveries[0].Seqs, []int{6, 7}) || deliveries[0].Attempts != 2 || deliveries[0].AcceptedAt.IsZero() {
		t.Fatalf("reopened resolved delivery: %+v, %v", deliveries, err)
	}
	sessions, err = j.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].Lost == nil || *sessions[0].Lost != resolvedLost || !sessions[0].Turned || sessions[0].Boot != "boot-old" {
		t.Fatalf("reopened session: %+v, %v", sessions, err)
	}
	ref = resolved
	now := time.Date(2026, 10, 1, 12, 1, 0, 0, time.UTC)
	id, err := j.AddDelivery(ctx, delivery.Delivery{
		Agent: ref, Session: sessions[0].Key,
		State: delivery.StatePending, Seqs: []int{8}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || id <= 17 {
		t.Fatalf("new delivery id %d, want greater than preserved id 17: %v", id, err)
	}
}

func TestReadModesKeepsHistoricalJournalReadOnly(t *testing.T) {
	ctx := context.Background()
	path := historicalJournal(t, 7)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO modes VALUES ('https://team.example', 'docs', 'reviewer', 'off')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	modes, err := ReadModes(ctx, path)
	ref := delivery.AgentRef{Server: "https://team.example", Board: "docs", Name: "reviewer"}
	if err != nil || len(modes) != 1 || modes[ref] != delivery.ModeOff {
		t.Fatalf("historical modes: %v, %v", modes, err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("read-only schema %d: %v", version, err)
	}
}
