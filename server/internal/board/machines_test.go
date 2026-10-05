package board_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

func wantMachineRequestInvalid(t *testing.T, what string, err error) {
	t.Helper()
	if e, ok := apierr.As(err); !ok || e.Code != "machine_request_invalid" {
		t.Fatalf("%s: got %v, want machine_request_invalid", what, err)
	}
}

// A request that expires while its collection waits for the write lock gives no key:
// the collection reads the time inside its transaction.
func TestAMachineRequestExpiringWhileTheCollectionWaitsGivesNoKey(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	person := w.auth(t, w.owner)
	r, err := w.svc.StartMachineRequest(ctx, "desktop", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ApproveMachineRequest(ctx, person, r.Code); err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(board.MachineRequestTTL - time.Second)
	err = w.queued(2*time.Second, func() error {
		_, err := w.svc.CollectMachineRequest(ctx, r.Secret)
		return err
	})
	wantMachineRequestInvalid(t, "collecting", err)
	keys, err := w.svc.ListKeys(ctx, w.auth(t, w.owner), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.Keys) != 1 {
		t.Fatalf("keys after the refused collection: %d, want 1", len(keys.Keys))
	}
}

// A request that expires while its approval waits for the write lock isn't approved.
func TestAMachineRequestExpiringWhileTheApprovalWaitsIsntApproved(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	person := w.auth(t, w.owner)
	r, err := w.svc.StartMachineRequest(ctx, "desktop", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(board.MachineRequestTTL - time.Second)
	err = w.queued(2*time.Second, func() error {
		_, err := w.svc.ApproveMachineRequest(ctx, person, r.Code)
		return err
	})
	wantMachineRequestInvalid(t, "approving", err)
}
