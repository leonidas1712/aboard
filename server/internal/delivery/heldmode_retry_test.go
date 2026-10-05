package delivery_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestFailedModeWriteRetriesTheSameServerRevision(t *testing.T) {
	r := newRig(t)
	r.setMode(reviewer, delivery.ModeAll)
	db, err := sql.Open("sqlite", r.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, query := range []string{
		`CREATE TRIGGER fail_mode_insert BEFORE INSERT ON modes BEGIN SELECT RAISE(FAIL, 'mode write refused'); END`,
		`CREATE TRIGGER fail_mode_update BEFORE UPDATE ON modes BEGIN SELECT RAISE(FAIL, 'mode write refused'); END`,
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}

	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.server.SetHeldMode(reviewer, delivery.ModeOff)
	r.modeIs(reviewer, delivery.ModeOff)
	modes, err := r.journal.Modes(ctx)
	if err != nil || modes[reviewer] != delivery.ModeAll {
		t.Fatalf("failed write changed the journal: %v, %v", modes, err)
	}
	for _, query := range []string{`DROP TRIGGER fail_mode_insert`, `DROP TRIGGER fail_mode_update`} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}

	// A new message rereads the same mode and revision, without changing either.
	r.post(reviewer, "the mode write can succeed now", false)
	r.eventually("the unchanged server mode to be saved again", 0, func() bool {
		modes, err := r.journal.Modes(ctx)
		return err == nil && modes[reviewer] == delivery.ModeOff
	})
	if got := r.setMode(reviewer, "").Mode; got != delivery.ModeOff {
		t.Fatalf("failed persistence replaced the server's mode: %s", got)
	}
}
