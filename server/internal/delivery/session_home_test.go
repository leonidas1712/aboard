package delivery_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSessionHomeSurvivesRestartAndIgnoresStaleHooks(t *testing.T) {
	r := newRig(t)
	oldHome, newHome := filepath.Join(t.TempDir(), "old"), filepath.Join(t.TempDir(), "new")
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "home", Boot: "old", CodexHome: oldHome})
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "home", Boot: "new", CodexHome: newHome})
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "home", Boot: "old", CodexHome: oldHome, RuntimeHook: true})
	r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "home"})
	r.restart()
	r.status()
	records, err := r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 || records[0].CodexHome != newHome {
		t.Fatalf("session home after omitted field, stale hook and restart: %+v, %v", records, err)
	}
}
