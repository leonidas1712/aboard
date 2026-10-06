package delivery_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// A Codex session a daemon from before combined handoffs kept open has no boot in the
// journal. After the upgrade it is still delivered to: the daemon gives it a boot the
// first time it prepares a handoff for it, instead of failing that handoff forever.
func TestSessionKeptWithoutABootIsDeliveredToAfterAnUpgrade(t *testing.T) {
	r, log := handoffRig(t)
	r.bind("codex", "t1", reviewer)
	r.stop()
	journalTrigger(t, r, `UPDATE sessions SET boot = '' WHERE harness = 'codex' AND session_id = 't1'`)
	r.start()

	seq := r.post(reviewer, "after the upgrade", false)
	r.eventually("the bundle", 500*time.Millisecond, func() bool { return len(r.codex.Handed("t1")) > 0 })
	if got := r.codex.Handed("t1"); !strings.Contains(got[0], "after the upgrade") {
		t.Fatalf("want the message handed over, got %q", got)
	}
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
	if log.contains("prepare handoff") {
		t.Fatal("the handoff failed before it was delivered")
	}
	if p := seatProblem(r.status(), reviewer); p != "" {
		t.Fatalf("the delivered seat shows %q", p)
	}
	r.stop()
	if boot := sessionBoot(t, r, "codex", "t1"); boot == "" {
		t.Fatal("the session's boot wasn't recorded")
	}
}

// A handoff that keeps failing to prepare is logged once, tried again with backoff,
// and shown in status until it succeeds.
func TestHandoffThatKeepsFailingIsReportedOnceAndShownInStatus(t *testing.T) {
	r, log := handoffRig(t)
	r.bind("codex", "t1", reviewer)
	journalTrigger(t, r, `CREATE TRIGGER reject_prepare BEFORE INSERT ON handoffs BEGIN SELECT RAISE(ABORT,'rejected_prepare'); END`)
	seq := r.post(reviewer, "kept back", false)
	r.eventually("the failure", 500*time.Millisecond, func() bool { return log.contains("prepare handoff") })
	if p := seatProblem(r.status(), reviewer); p != delivery.ReasonHandoffFailed {
		t.Fatalf("a seat whose handoff can't be prepared shows %q, want %s", p, delivery.ReasonHandoffFailed)
	}
	// Ten minutes of failures: with backoff that is a handful of tries, logged once.
	for range 20 {
		r.clock.Advance(30 * time.Second)
		<-time.After(2 * time.Millisecond) // a poll interval, not a wait for the daemon
	}
	if n := log.count(`msg="prepare handoff"`); n != 1 {
		t.Fatalf("a failing handoff was logged %d times, want once", n)
	}
	if len(r.codex.Handed("t1")) != 0 {
		t.Fatal("a failed preparation handed text")
	}

	journalTrigger(t, r, `DROP TRIGGER reject_prepare`)
	r.eventually("the bundle", 30*time.Second, func() bool { return len(r.codex.Handed("t1")) > 0 })
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
	if p := seatProblem(r.status(), reviewer); p != "" {
		t.Fatalf("the seat still shows %q after its handoff succeeded", p)
	}
}

func sessionBoot(t *testing.T, r *rig, harness, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", r.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var boot string
	if err := db.QueryRowContext(context.Background(), `SELECT boot FROM sessions WHERE harness = ? AND session_id = ?`, harness, id).Scan(&boot); err != nil {
		t.Fatal(err)
	}
	return boot
}

func (l *handoffLog) count(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.text, text)
}
