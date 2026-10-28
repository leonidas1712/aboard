package delivery

import (
	"context"
	"time"
)

// WarningNotWaiting is the warning the daemon reports for a session that is open and
// idle with nothing waiting to wake it.
const WarningNotWaiting = "not_waiting"

// notWaitingAfter is how long such a session goes without a waiter before the warning:
// a turn's end and its stop hook's wait arrive a moment apart, and waiting hooks
// reconnect within a second of a daemon restart.
const notWaitingAfter = 5 * time.Second

// validDeliveryWarning reports whether a client may report the warning. not_waiting is
// the daemon's own.
func validDeliveryWarning(reason string) bool {
	return reason == "" || reason == "codex_no_daemon" || reason == "codex_no_daemon_orca"
}

func (s *session) noteDeliveryWarning(ctx context.Context, req Request, currentHook bool) {
	if s.key.Harness != "codex" || req.Subagent != "" || !req.DeliveryChecked ||
		!validDeliveryWarning(req.DeliveryWarning) || req.DeliveryWarning == s.deliveryWarning {
		return
	}
	if req.Op != OpRegister && req.Op != OpBind && !currentHook && (req.RuntimeHook || req.Boot != "") {
		return
	}
	s.deliveryWarning = req.DeliveryWarning
	s.saveSession(ctx)
}

// PresenceWarningServer reports optional diagnostic metadata without changing the
// required presence port for older adapters.
type PresenceWarningServer interface {
	SetPresenceWarning(context.Context, AgentRef, Presence, Mode, string) error
}
