package delivery

import "context"

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
