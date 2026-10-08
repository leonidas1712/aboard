package retry

import (
	"testing"
	"time"
)

func TestReconnectDelaysStayPositiveAndWithinTheBackoff(t *testing.T) {
	for _, base := range []time.Duration{time.Second, 30 * time.Second, time.Minute} {
		lo, middle, hi := jitter(base, 0), jitter(base, 0.5), jitter(base, 1)
		if lo != base/2 || hi != base || lo >= middle || middle >= hi {
			t.Fatalf("base %s: delays %s, %s, %s", base, lo, middle, hi)
		}
	}
}
