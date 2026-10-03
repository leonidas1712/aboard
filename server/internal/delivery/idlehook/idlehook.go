// Package idlehook is the delivery adapter for harnesses whose hook waits while the
// session is idle, as Claude Code's stop hook does. Such a harness has no command to put
// text into a running session; instead the hook, run when a turn ends, waits on the
// daemon, and a bundle handed to it wakes the session (Claude Code: written to the
// hook's standard error with exit code 2, on a hook marked asyncRewake).
package idlehook

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Adapter hands bundles to sessions through their waiting hook.
type Adapter struct {
	// Name is the harness's name, such as "claude-code".
	Name string
}

var _ delivery.Adapter = Adapter{}

// Harness returns the harness's name.
func (a Adapter) Harness() string { return a.Name }

// Validate accepts any session id: the session-start hook reports the exact id, and
// sub-agents run inside their parent's session rather than as sessions of their own.
func (Adapter) Validate(_ context.Context, sessionID string) error {
	if sessionID == "" {
		return delivery.ErrTargetAbsent
	}
	return nil
}

// WaitsForIdle is true: a bundle goes only to a waiting hook.
func (Adapter) WaitsForIdle() bool { return true }

// Hand gives the bundle to the waiting hook. Confirmation comes later, from the
// session's next event.
func (Adapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	if h.Waiter == nil {
		return false, delivery.ErrBusy
	}
	return false, h.Waiter.Deliver(ctx, h.ID, h.Bundle)
}
