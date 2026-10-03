package delivery

import (
	"context"
	"io"
	"net"
	"testing"
)

// A stop hook says it has the bundle and exits at once, so by the time the daemon looks,
// the hook has both taken the bundle and gone. The bundle reached the session: it is
// handed, not busy, or the next waiting hook would get it again.
func TestHookThatTookTheBundleAndLeftHasIt(t *testing.T) {
	for range 100 {
		daemonEnd, hookEnd := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, hookEnd) }()
		w := &waiter{conn: daemonEnd, received: make(chan struct{}, 1), gone: make(chan struct{})}
		w.received <- struct{}{}
		close(w.gone)
		err := w.Deliver(context.Background(), "the bundle")
		_ = daemonEnd.Close()
		_ = hookEnd.Close()
		if err != nil {
			t.Fatalf("Deliver to a hook that took the bundle and then left = %v, want nil", err)
		}
	}
}
