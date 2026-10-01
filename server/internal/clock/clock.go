// Package clock gives code that depends on the time a replaceable source of it, so tests
// can control expiry, long-poll timeouts and timestamps exactly.
package clock

import (
	"sync"
	"time"
)

// Clock tells the time and makes timers.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Real is the system clock.
type Real struct{}

// Now returns the current time.
func (Real) Now() time.Time { return time.Now() }

// After waits for d on the system clock.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a clock that only moves when Advance is called.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After fires once Advance moves the clock d or more past the current fake time.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	at := f.now.Add(d)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, fakeWaiter{at: at, ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires every timer that is now due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(f.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- f.now
	}
	f.waiters = kept
}
