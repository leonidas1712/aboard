package delivery

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// queueingAdapter is a harness that queues bundles itself, as Codex does: such a
// session can take a bundle whenever it isn't in a turn.
type queueingAdapter struct{}

func (queueingAdapter) Harness() string                              { return "codex" }
func (queueingAdapter) Validate(context.Context, string) error       { return nil }
func (queueingAdapter) WaitsForIdle() bool                           { return false }
func (queueingAdapter) Hand(context.Context, Handover) (bool, error) { return true, nil }

// A session that can't deliver yet must not keep a passed gather deadline on its timer:
// tryDeliver leaves the deadline as it is, so the timer would fire again at once and the
// session's loop would spin until the session opened.
func TestNextTimerSkipsGatherWhileDeliveryCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		open       bool
		refreshing map[int64]bool
	}{
		{"closed", false, map[int64]bool{}},
		{"refreshing", true, map[int64]bool{7: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clock.NewFake(time.Unix(1000, 0))
			s := &session{d: &Daemon{cfg: Config{Clock: c}}, adapter: queueingAdapter{}, open: tc.open, refreshing: tc.refreshing}
			s.gatherUntil = c.Now().Add(-time.Second)
			if !s.canTake() {
				t.Fatal("a queueing harness outside a turn should be able to take a bundle")
			}
			s.tryDeliver(context.Background())
			if next := s.nextTimer(); !next.IsZero() && !next.After(c.Now()) {
				t.Fatalf("nextTimer = %s, already passed at %s: the session would wake again at once", next, c.Now())
			}
		})
	}
}

// Once the session can deliver, a passed gather deadline still wakes it: that is how a
// gathered bundle gets handed.
func TestNextTimerKeepsGatherWhenDeliveryCanRun(t *testing.T) {
	c := clock.NewFake(time.Unix(1000, 0))
	s := &session{d: &Daemon{cfg: Config{Clock: c}}, adapter: queueingAdapter{}, open: true, refreshing: map[int64]bool{}}
	s.gatherUntil = c.Now().Add(-time.Second)
	if next := s.nextTimer(); !next.Equal(s.gatherUntil) {
		t.Fatalf("nextTimer = %s, want the gather deadline %s", next, s.gatherUntil)
	}
}
