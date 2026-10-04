package cli

import (
	"context"
	"errors"
	"strconv"
	"syscall"
	"time"
)

// exitWithVar names a process this aboard process must not outlive. Tests set it to
// their own process id, so a local server, delivery daemon or hook they started stops
// when the test process dies, even when it is killed before it can stop them. It is
// unset for everyone else, and then nothing changes.
const exitWithVar = "ABOARD_EXIT_WITH_PID"

// exitWithCheckInterval is how often a process started with exitWithVar checks that the
// process it names still runs.
const exitWithCheckInterval = 200 * time.Millisecond

// exitWith returns a context that is canceled once the process named by value has
// exited, and a function that stops watching it. Canceling the context stops the
// server and the daemon the same way an interrupt does. An empty or malformed value
// watches nothing.
func exitWith(ctx context.Context, value string) (watched context.Context, stop func()) {
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(exitWithCheckInterval)
		defer tick.Stop()
		for processRuns(pid) {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
		cancel()
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}

// processRuns reports whether a process with this id exists. A process owned by
// another user still exists: signal 0 then fails with EPERM.
func processRuns(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
