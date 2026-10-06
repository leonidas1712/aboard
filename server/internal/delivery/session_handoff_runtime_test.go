package delivery_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type handoffLog struct {
	mu   sync.Mutex
	text string
}

func (l *handoffLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.text += string(p)
	return len(p), nil
}

func (l *handoffLog) contains(text string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Contains(l.text, text)
}

func journalTrigger(t *testing.T, r *rig, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", r.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.ExecContext(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func handoffRig(t *testing.T) (*rig, *handoffLog) {
	t.Helper()
	r := newRig(t)
	r.ok(delivery.Request{Op: delivery.OpStatus})
	r.stop()
	log := &handoffLog{}
	r.configure = func(cfg *delivery.Config) { cfg.Log = slog.New(slog.NewTextHandler(io.Writer(log), nil)) }
	r.start()
	r.ok(delivery.Request{Op: delivery.OpStatus})
	t.Cleanup(func() {
		if t.Failed() {
			log.mu.Lock()
			defer log.mu.Unlock()
			t.Log(log.text)
		}
	})
	return r, log
}

func TestHandoffPreparationFailureExposesNoTextOrReceipt(t *testing.T) {
	r, log := handoffRig(t)
	a := reviewer
	a.MemberID = "mem_prepare"
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "prepare", Boot: "boot-prepare"})
	r.bind("codex", "prepare", a)
	journalTrigger(t, r, `CREATE TRIGGER reject_prepare BEFORE INSERT ON handoffs BEGIN SELECT RAISE(ABORT,'rejected_prepare'); END`)
	r.postFromOwner(a, "must not be handed")
	r.eventually("preparation outcome", 500*time.Millisecond, func() bool { return log.contains("prepare handoff") || len(r.codex.Handed("prepare")) > 0 })
	if len(r.codex.Handed("prepare")) != 0 || r.server.Cursor(a) != 0 {
		t.Fatal("failed preparation handed text or advanced cursor")
	}
	rows, err := r.journal.Deliveries(context.Background(), delivery.StateHanded, delivery.StateConfirmed)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial preparation retained delivery rows: %+v, %v", rows, err)
	}
	journalTrigger(t, r, `DROP TRIGGER reject_prepare`)
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "prepare"})
	r.eventually("retry after journal recovery", 500*time.Millisecond, func() bool { return r.server.Cursor(a) == 1 })
	if len(r.codex.Handed("prepare")) != 1 {
		t.Fatalf("recovered preparation handed %d times", len(r.codex.Handed("prepare")))
	}
}

func TestHandoffConfirmationFailureKeepsReceivedTextUnacknowledged(t *testing.T) {
	r, log := handoffRig(t)
	a := reviewer
	a.MemberID = "mem_confirm"
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "confirm", Boot: "boot-confirm"})
	r.bind("codex", "confirm", a)
	journalTrigger(t, r, `CREATE TRIGGER reject_confirmation BEFORE UPDATE OF state ON deliveries WHEN NEW.state='confirmed' BEGIN SELECT RAISE(ABORT,'rejected_confirm'); END`)
	r.postFromOwner(a, "already in the harness")
	r.eventually("confirmation outcome", 500*time.Millisecond, func() bool { return log.contains("confirm handoff") })
	if len(r.codex.Handed("confirm")) != 1 || r.server.Cursor(a) != 0 {
		t.Fatal("failed confirmation repeated payload or acknowledged it")
	}
	rows, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed confirmation became durable: %+v, %v", rows, err)
	}
	journalTrigger(t, r, `DROP TRIGGER reject_confirmation`)
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "codex", Session: "confirm"})
	r.eventually("confirmation retry", 0, func() bool { return r.server.Cursor(a) == 1 })
	if len(r.codex.Handed("confirm")) != 1 {
		t.Fatal("confirmation retry re-handed the accepted payload")
	}
}

func TestVerifiedDirectQueueBindingGetsADurableBootBeforeDelivery(t *testing.T) {
	r := newRig(t)
	a := reviewer
	a.MemberID = "mem_direct"
	r.bind("codex", "direct", a)
	records, err := r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 || !strings.HasPrefix(records[0].Boot, "boot_") {
		t.Fatalf("direct bind lacked durable boot: %+v, %v", records, err)
	}
	r.postFromOwner(a, "direct queue works")
	r.eventually("direct handoff", 500*time.Millisecond, func() bool { return r.server.Cursor(a) == 1 })
	manifests, err := r.journal.Handoffs(context.Background())
	if err != nil || len(manifests) != 1 || manifests[0].Boot != records[0].Boot {
		t.Fatalf("handoff escaped its boot: %+v, %v", manifests, err)
	}
}
