// Package extension is the delivery adapter for harnesses that Aboard reaches through an
// extension running inside the harness's own process, such as omp's. The extension holds
// a connection to the daemon for as long as its session runs (spec/control.md, the
// extension connection); while the session is idle that connection is its waiter, and a
// bundle handed to it is confirmed when the extension answers that it added it.
package extension

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Adapter hands bundles to sessions through their extension's connection.
type Adapter struct {
	// Name is the harness's name, such as "omp".
	Name string
}

var _ delivery.Adapter = Adapter{}

// Harness returns the harness's name.
func (a Adapter) Harness() string { return a.Name }

// Validate accepts any session id: the extension reports its session's exact id, and
// subagents never connect.
func (Adapter) Validate(_ context.Context, sessionID string) error {
	if sessionID == "" {
		return delivery.ErrTargetAbsent
	}
	return nil
}

// WaitsForIdle is true: a bundle goes only over the connection of an idle session.
func (Adapter) WaitsForIdle() bool { return true }

// Hand sends the bundle over the extension's connection. The extension's answer that it
// added the bundle to its session confirms it, as a harness's queue taking a bundle does.
func (Adapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	if h.Waiter == nil {
		return false, delivery.ErrBusy
	}
	if w, ok := h.Waiter.(delivery.HandoffWaiter); ok {
		if err := w.DeliverHandoff(ctx, h); err != nil {
			return false, err
		}
		return true, nil
	}
	if h.HandoffID != "" {
		return false, delivery.ErrExtensionOutdated
	}
	if err := h.Waiter.Deliver(ctx, h.ID, h.Bundle); err != nil {
		return false, err
	}
	return true, nil
}
