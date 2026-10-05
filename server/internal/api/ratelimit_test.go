package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func fakeClock() *clock.Fake { return clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)) }

// The attempt limit counts every attempt within a minute, and starts afresh with the
// next minute.
func TestAttemptLimitCountsEachMinuteOnItsOwn(t *testing.T) {
	clk := fakeClock()
	l := newRateLimiter(clk, 2)
	for i := range 2 {
		if !l.allow("peer") {
			t.Fatalf("attempt %d within the limit was refused", i+1)
		}
	}
	if l.allow("peer") {
		t.Fatal("a third attempt in the minute was allowed")
	}
	if !l.allow("other") {
		t.Fatal("another address shares the per-address count")
	}
	clk.Advance(59 * time.Second)
	if l.allow("peer") {
		t.Fatal("an attempt before the minute ended was allowed")
	}
	clk.Advance(time.Second)
	if !l.allow("peer") {
		t.Fatal("the first attempt of the next minute was refused")
	}
}

// The failure limit counts a failure in the minute it failed in, never in an earlier
// one, and checking it counts nothing.
func TestFailureLimitCountsFailuresInTheMinuteTheyHappen(t *testing.T) {
	clk := fakeClock()
	l := newRateLimiter(clk, 1)
	for range 5 {
		if l.full("peer") {
			t.Fatal("checking the limit counted as a failure")
		}
	}
	l.fail("peer")
	if !l.full("peer") {
		t.Fatal("one failure didn't reach a limit of one")
	}
	clk.Advance(time.Minute)
	if l.full("peer") {
		t.Fatal("a failure from the last minute still counts")
	}
	l.fail("peer")
	if !l.full("peer") {
		t.Fatal("a failure in the new minute didn't count")
	}
}

// From the security review: a success in one minute that finishes in the next must not
// take back a failure counted in the next. A success counts nothing toward the failure
// limit, so there is nothing to take back.
func TestASuccessFinishingInTheNextMinuteKeepsItsFailures(t *testing.T) {
	clk := fakeClock()
	limits := signInLimits{
		failed:   connectLimits{perAddr: newRateLimiter(clk, 1), all: newRateLimiter(clk, 100)},
		attempts: connectLimits{perAddr: newRateLimiter(clk, 100), all: newRateLimiter(clk, 100)},
	}
	r := &http.Request{RemoteAddr: "192.0.2.1:5000"}
	if !limits.allow(r) {
		t.Fatal("first attempt refused")
	}
	clk.Advance(time.Minute)
	if !limits.allow(r) {
		t.Fatal("first attempt of the new minute refused")
	}
	limits.failedAttempt(r) // it fails; the earlier one then succeeds, which counts nothing
	if limits.allow(r) {
		t.Fatal("a second guess in the minute was allowed past a failure limit of one")
	}
}

// Successes never reach the failure limit, but the higher attempt limit still bounds
// them.
func TestSuccessfulSignInsMeetOnlyTheAttemptLimit(t *testing.T) {
	clk := fakeClock()
	limits := signInLimits{
		failed:   connectLimits{perAddr: newRateLimiter(clk, 1), all: newRateLimiter(clk, 1)},
		attempts: connectLimits{perAddr: newRateLimiter(clk, 3), all: newRateLimiter(clk, 100)},
	}
	r := &http.Request{RemoteAddr: "192.0.2.1:5000"}
	for i := range 3 {
		if !limits.allow(r) {
			t.Fatalf("successful attempt %d refused", i+1)
		}
	}
	if limits.allow(r) {
		t.Fatal("a fourth attempt in the minute was allowed past the attempt limit")
	}
}
