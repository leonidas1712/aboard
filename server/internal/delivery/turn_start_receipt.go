package delivery

import "context"

func (s *session) receiveTurnStart(ctx context.Context, req Request) Response {
	h := s.handoffs[req.HandoffID]
	if h == nil || h.manifest.Class != ClassTurnStart || h.manifest.Session != s.key || req.Boot == "" || req.Boot != s.boot || h.manifest.Boot != s.boot {
		return errorResponse("invalid_request", "This turn-start receipt does not match the session's handoff.", "Keep the bundle queued; use the handoff id and boot returned with its text.")
	}
	confirmed, err := s.confirmHandoff(ctx, h)
	if err != nil {
		return errorResponse("internal", "Couldn't save the turn-start receipt.", "The unconfirmed bundle stays available for recovery.")
	}
	s.logConfirmed(confirmed)
	for _, a := range s.agents {
		s.maybeAck(a)
	}
	return Response{V: ProtocolVersion}
}

func (s *session) requeueUnreceivedTurnStarts(ctx context.Context) {
	for _, h := range s.handoffs {
		if h.manifest.Class != ClassTurnStart {
			continue
		}
		for _, p := range h.manifest.Parts {
			if a := s.agents[p.Agent.Key()]; a != nil {
				if dl := a.deliveries[p.DeliveryID]; dl != nil && dl.HandoffID == h.manifest.ID && dl.State == StateHanded {
					s.setState(ctx, dl, StatePending)
				}
			}
		}
	}
}
