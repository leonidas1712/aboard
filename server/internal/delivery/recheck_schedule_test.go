package delivery

import (
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestAdmissionBackoffGrowsAndCapsWithoutZeroDelay(t *testing.T) {
	c := clock.NewFake(time.Unix(0, 0))
	s := session{d: &Daemon{cfg: Config{Clock: c}}}
	ceilings := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute}
	for i := 0; i < 1000; i++ {
		s.deferRecheck()
		ceiling := ceilings[min(i, len(ceilings)-1)]
		delay := s.recheckAt.Sub(c.Now())
		if delay < ceiling/2 || delay > ceiling {
			t.Fatalf("failure %d waits %s, want %s..%s", i+1, delay, ceiling/2, ceiling)
		}
		c.Advance(delay)
	}
}
