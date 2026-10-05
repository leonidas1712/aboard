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
	r, err := w.svc.StartMachineRequest(ctx, "alex", "desktop", "127.0.0.1")
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
	r, err := w.svc.StartMachineRequest(ctx, "alex", "desktop", "127.0.0.1")
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

// The approving key expiring while the collection waits for the write lock gives no
// key: the collection rechecks it with the time read inside its transaction.
func TestAnApprovingKeyExpiringWhileTheCollectionWaitsGivesNoKey(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	w.sql(t, "UPDATE access_keys SET expires_at = '2026-10-01T16:00:30.000Z'")
	r, err := w.svc.StartMachineRequest(ctx, "alex", "desktop", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ApproveMachineRequest(ctx, w.auth(t, w.owner), r.Code); err != nil {
		t.Fatal(err)
	}
	err = w.queued(40*time.Second, func() error {
		_, err := w.svc.CollectMachineRequest(ctx, r.Secret)
		return err
	})
	wantMachineRequestInvalid(t, "collecting", err)
	w.sql(t, "UPDATE access_keys SET expires_at = NULL")
	keys, err := w.svc.ListKeys(ctx, w.auth(t, w.owner), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.Keys) != 1 {
		t.Fatalf("keys after the refused collection: %d, want 1", len(keys.Keys))
	}
}

// Collections racing for one approved request: exactly one gets a key.
func TestRacingCollectionsGiveOneKey(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	r, err := w.svc.StartMachineRequest(ctx, "alex", "desktop", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.ApproveMachineRequest(ctx, w.auth(t, w.owner), r.Code); err != nil {
		t.Fatal(err)
	}
	const racers = 8
	start := make(chan struct{})
	errs := make(chan error, racers)
	for range racers {
		go func() {
			<-start
			_, err := w.svc.CollectMachineRequest(ctx, r.Secret)
			errs <- err
		}()
	}
	close(start)
	won := 0
	for range racers {
		if err := <-errs; err == nil {
			won++
		} else {
			wantMachineRequestInvalid(t, "a losing collection", err)
		}
	}
	keys, err := w.svc.ListKeys(ctx, w.auth(t, w.owner), "")
	if err != nil {
		t.Fatal(err)
	}
	if won != 1 || len(keys.Keys) != 2 {
		t.Fatalf("%d collections won and %d keys exist, want 1 and 2", won, len(keys.Keys))
	}
}

// A request names its person: anyone else with the right code is refused exactly as a
// wrong code is, and the request stays pending for its person. A request for a handle
// nobody has can be started, and never approved.
func TestOnlyTheNamedPersonDecidesAMachineRequest(t *testing.T) {
	w := newKeyWorld(t)
	ctx := context.Background()
	owner := w.auth(t, w.owner)
	inv, err := w.svc.CreateServerInvite(ctx, owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	sam, err := w.svc.Connect(ctx, board.ConnectInput{Invite: inv.Secret, Handle: "sam", KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	stranger := w.auth(t, sam.Token)
	for _, handle := range []string{"alex", "nobody"} {
		r, err := w.svc.StartMachineRequest(ctx, handle, "desktop", "127.0.0.1")
		if err != nil {
			t.Fatalf("starting a request for %s: %v", handle, err)
		}
		_, err = w.svc.LookupMachineRequest(ctx, stranger, r.Code)
		wantMachineRequestInvalid(t, "a stranger looking up "+handle+"'s request", err)
		_, err = w.svc.ApproveMachineRequest(ctx, stranger, r.Code)
		wantMachineRequestInvalid(t, "a stranger approving "+handle+"'s request", err)
		_, err = w.svc.RefuseMachineRequest(ctx, stranger, r.Code)
		wantMachineRequestInvalid(t, "a stranger refusing "+handle+"'s request", err)
		if c, err := w.svc.CollectMachineRequest(ctx, r.Secret); err != nil || !c.Pending {
			t.Fatalf("%s's request after the stranger: %+v %v, want pending", handle, c, err)
		}
		if handle == "alex" {
			if _, err := w.svc.ApproveMachineRequest(ctx, owner, r.Code); err != nil {
				t.Fatalf("alex approving his own request: %v", err)
			}
		}
	}
}
