package delivery_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestFailedModeWriteRetriesTheSameServerRevision(t *testing.T) {
	r := newRig(t)
	r.setMode(reviewer, delivery.ModeAll)
	ctx := context.Background()
	journalTrigger(t, r, `CREATE TRIGGER fail_mode_insert BEFORE INSERT ON modes BEGIN SELECT RAISE(FAIL, 'mode write refused'); END`)
	journalTrigger(t, r, `CREATE TRIGGER fail_mode_update BEFORE UPDATE ON modes BEGIN SELECT RAISE(FAIL, 'mode write refused'); END`)

	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.server.SetHeldMode(reviewer, delivery.ModeOff)
	r.modeIs(reviewer, delivery.ModeOff)
	modes, err := r.journal.Modes(ctx)
	if err != nil || modes[reviewer] != delivery.ModeAll {
		t.Fatalf("failed write changed the journal: %v, %v", modes, err)
	}
	journalTrigger(t, r, `DROP TRIGGER fail_mode_insert`)
	journalTrigger(t, r, `DROP TRIGGER fail_mode_update`)

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
