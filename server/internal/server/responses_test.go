package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type responseCleanupFixture struct {
	purges chan struct{}
	waits  chan time.Duration
	ticks  chan time.Time
}

func (f *responseCleanupFixture) PurgeResponses(context.Context) error {
	f.purges <- struct{}{}
	return errors.New("storage temporarily unavailable")
}

func (f *responseCleanupFixture) Now() time.Time { return time.Time{} }

func (f *responseCleanupFixture) After(d time.Duration) <-chan time.Time {
	f.waits <- d
	return f.ticks
}

func TestResponseCleanupRunsAtStartupRetriesHourlyAndStops(t *testing.T) {
	t.Parallel()
	f := &responseCleanupFixture{purges: make(chan struct{}, 2), waits: make(chan time.Duration, 2), ticks: make(chan time.Time, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cleanResponses(ctx, f, f, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("cleanup did not stop")
		}
	})
	for i := range 2 {
		select {
		case <-f.purges:
		case <-time.After(5 * time.Second):
			t.Fatal("cleanup did not run")
		}
		select {
		case wait := <-f.waits:
			if wait != time.Hour {
				t.Fatalf("cleanup interval %s, want one hour", wait)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cleanup did not schedule its next run")
		}
		if i == 0 {
			f.ticks <- time.Time{}
		}
	}
}
