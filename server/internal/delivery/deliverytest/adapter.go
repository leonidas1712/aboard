package deliverytest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// AdapterFixture is one harness, prepared for the adapter contract suite.
type AdapterFixture struct {
	// New returns the adapter under test.
	New func(t *testing.T) delivery.Adapter
	// Root returns the id of a root session that exists.
	Root func(t *testing.T) string
	// SubAgent returns the id of a sub-agent session, or "" for a harness without them.
	SubAgent func(t *testing.T) string
	// Absent returns the id of a session that doesn't exist, or "" for a harness that
	// can't tell.
	Absent func(t *testing.T) string
	// Received returns the bundles a harness that queues took for a session, in order.
	// It is unused for a harness that waits for idle.
	Received func(t *testing.T, sessionID string) []string
}

// trickyBundle has text a shell would act on, so the suite proves no shell sees it.
const trickyBundle = "<aboard-messages board=\"docs\" count=\"1\">\n" +
	"<aboard-message board=\"docs\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\">\n" +
	"It's \"done\"; run $(rm -rf ~) `whoami` && echo $HOME | cat > /tmp/x\n--flag -n\n" +
	"</aboard-message>\n</aboard-messages>"

// RunAdapter runs the Adapter contract suite.
func RunAdapter(t *testing.T, f AdapterFixture) {
	ctx := context.Background()

	t.Run("ValidateAcceptsARootSession", func(t *testing.T) {
		if err := f.New(t).Validate(ctx, f.Root(t)); err != nil {
			t.Fatalf("Validate(root) = %v", err)
		}
	})

	t.Run("ValidateRefusesASubAgent", func(t *testing.T) {
		id := f.SubAgent(t)
		if id == "" {
			t.Skip("this harness has no sub-agent sessions")
		}
		if err := f.New(t).Validate(ctx, id); !errors.Is(err, delivery.ErrSubAgent) {
			t.Fatalf("Validate(sub-agent) = %v, want ErrSubAgent", err)
		}
	})

	t.Run("ValidateRefusesAnAbsentSession", func(t *testing.T) {
		id := f.Absent(t)
		if id == "" {
			t.Skip("this harness can't tell whether a session exists")
		}
		if err := f.New(t).Validate(ctx, id); !errors.Is(err, delivery.ErrTargetAbsent) {
			t.Fatalf("Validate(absent) = %v, want ErrTargetAbsent", err)
		}
	})

	t.Run("HandDeliversTheBundleUnchanged", func(t *testing.T) {
		a, id := f.New(t), f.Root(t)
		w := &RecordingWaiter{}
		var waiter delivery.Waiter
		if a.WaitsForIdle() {
			waiter = w
		}
		confirmed, err := a.Hand(ctx, delivery.Handover{SessionID: id, Bundle: trickyBundle, Waiter: waiter})
		if err != nil {
			t.Fatalf("Hand = %v", err)
		}
		got := w.Bundles()
		if !a.WaitsForIdle() {
			got = f.Received(t, id)
		}
		if !slices.Equal(got, []string{trickyBundle}) {
			t.Fatalf("the harness got %q, want the bundle unchanged", got)
		}
		if confirmed == a.WaitsForIdle() {
			t.Fatalf("confirmed = %v: a harness that waits for idle confirms later; one that queues confirms on acceptance", confirmed)
		}
	})

	t.Run("HandWithoutAWaitingHookIsBusy", func(t *testing.T) {
		a := f.New(t)
		if !a.WaitsForIdle() {
			t.Skip("this harness queues bundles itself")
		}
		if _, err := a.Hand(ctx, delivery.Handover{SessionID: f.Root(t), Bundle: trickyBundle}); !errors.Is(err, delivery.ErrBusy) {
			t.Fatalf("Hand with no waiting hook = %v, want ErrBusy", err)
		}
	})

	t.Run("HandToAHookThatLeftIsBusy", func(t *testing.T) {
		a := f.New(t)
		if !a.WaitsForIdle() {
			t.Skip("this harness queues bundles itself")
		}
		w := &RecordingWaiter{Gone: true}
		if _, err := a.Hand(ctx, delivery.Handover{SessionID: f.Root(t), Bundle: trickyBundle, Waiter: w}); !errors.Is(err, delivery.ErrBusy) {
			t.Fatalf("Hand to a hook that left = %v, want ErrBusy, which never counts as a failed attempt", err)
		}
	})

	t.Run("HandToAnAbsentSessionFails", func(t *testing.T) {
		a := f.New(t)
		id := f.Absent(t)
		if a.WaitsForIdle() || id == "" {
			t.Skip("only a harness that queues can be handed a bundle for a session that is gone")
		}
		if _, err := a.Hand(ctx, delivery.Handover{SessionID: id, Bundle: trickyBundle}); !errors.Is(err, delivery.ErrTargetAbsent) {
			t.Fatalf("Hand(absent) = %v, want ErrTargetAbsent", err)
		}
	})
}

// RecordingWaiter is a waiting hook that records the bundles it is given.
type RecordingWaiter struct {
	mu sync.Mutex
	// Gone makes the waiter act like a hook whose connection closed.
	Gone     bool
	bundles  []string
	released int
}

// Deliver records the bundle, or returns ErrBusy when the hook is gone.
func (w *RecordingWaiter) Deliver(_ context.Context, bundle string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.Gone {
		return delivery.ErrBusy
	}
	w.bundles = append(w.bundles, bundle)
	return nil
}

// Release counts a release.
func (w *RecordingWaiter) Release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.released++
}

// Bundles returns the bundles delivered so far.
func (w *RecordingWaiter) Bundles() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.bundles)
}
