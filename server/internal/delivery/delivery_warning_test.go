package delivery_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestDeliveryWarningSurvivesRestartAndOnlyFreshChecksClearIt(t *testing.T) {
	r := newRig(t)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "warning", Boot: "old", DeliveryChecked: true, DeliveryWarning: "codex_no_daemon_orca"})
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "warning", Boot: "new"})
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "warning", Boot: "old", RuntimeHook: true, DeliveryChecked: true})
	r.restart()
	r.status()
	records, err := r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 || records[0].DeliveryWarning != "codex_no_daemon_orca" {
		t.Fatalf("warning after old client, stale hook and restart: %+v, %v", records, err)
	}
	r.ok(delivery.Request{Op: delivery.OpTurnEnd, Harness: "codex", Session: "warning", Boot: "new", RuntimeHook: true, DeliveryChecked: true})
	records, err = r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 || records[0].DeliveryWarning != "" {
		t.Fatalf("healthy check did not clear warning: %+v, %v", records, err)
	}
}

func TestAbsentCodexValidationAnnotatesOnlyTheExistingSeat(t *testing.T) {
	r := newRig(t)
	home := filepath.Join(t.TempDir(), "codex")
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "warning", Boot: "original", CodexHome: home})
	r.bind("codex", "warning", reviewer)
	r.codex.MarkAbsent("warning")
	req := delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "warning", Boot: "rejected", CodexHome: filepath.Join(t.TempDir(), "wrong"), DeliveryChecked: true, DeliveryWarning: "codex_no_daemon_orca"}
	if response := r.call(req); response.Error == nil || response.Error.Code != "codex_target_absent" {
		t.Fatalf("absent validation accepted: %+v", response)
	}
	records, err := r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 || records[0].Boot != "original" || records[0].CodexHome != home || !records[0].Open || records[0].DeliveryWarning != "codex_no_daemon_orca" {
		t.Fatalf("failed validation changed session authority: %+v, %v", records, err)
	}
	st := r.status()
	if len(st.Bindings) != 1 || st.Bindings[0].Agent != reviewer || len(st.DeliveryWarnings) != 1 || st.DeliveryWarnings[0].Reason != "codex_no_daemon_orca" {
		t.Fatalf("warning status: %+v", st)
	}
	r.codex.MarkAbsent("unknown")
	req.Session = "unknown"
	if response := r.call(req); response.Error == nil {
		t.Fatal("unknown absent thread accepted")
	}
	records, err = r.journal.Sessions(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("failed validation created a session: %+v, %v", records, err)
	}
}
