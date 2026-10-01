// Package claude is the delivery adapter for Claude Code. Claude Code has no command to
// put text into a running session; instead its stop hook, marked asyncRewake, waits on
// the daemon while the session is idle, and a bundle written to that hook's standard
// error with exit code 2 wakes the session with it.
package claude

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Adapter hands bundles to Claude Code sessions through their waiting stop hook.
type Adapter struct{}

var _ delivery.Adapter = Adapter{}

// Harness returns "claude-code".
func (Adapter) Harness() string { return delivery.HarnessClaudeCode }

// Validate accepts any session id: Claude Code's session-start hook reports the exact
// id, and sub-agents run inside their parent's session rather than as sessions of
// their own.
func (Adapter) Validate(_ context.Context, sessionID string) error {
	if sessionID == "" {
		return delivery.ErrTargetAbsent
	}
	return nil
}

// WaitsForIdle is true: a bundle goes only to a waiting stop hook.
func (Adapter) WaitsForIdle() bool { return true }

// Hand gives the bundle to the waiting stop hook. Confirmation comes later, from the
// session's next event.
func (Adapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	if h.Waiter == nil {
		return false, delivery.ErrBusy
	}
	return false, h.Waiter.Deliver(ctx, h.Bundle)
}
