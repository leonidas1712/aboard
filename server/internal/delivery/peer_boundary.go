package delivery

import "context"

func (s *session) peerBoundaryOffers(peers bool) []offer {
	owners := s.offers(ownerOnly)
	if !peers {
		return owners
	}
	used := map[AgentKey]bool{}
	for _, h := range s.handoffs {
		if h.manifest.PeerTurn == s.peerTurn {
			for _, sender := range h.manifest.PeerSenders {
				used[sender] = true
			}
		}
	}
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		mode := s.d.mode(ref)
		if a.adopting || !a.fetched || a.problem != "" || a.midturnPolicy != "my-agents" || mode == ModeOff || mode == ModeHumans || s.beingRead(ref) {
			continue
		}
		blocked := false
		for _, dl := range a.deliveries {
			if s.deliveryFullyShown(a, dl) {
				continue
			}
			if dl.State == StatePending || dl.State == StateHeld || dl.State == StateRetry || dl.State == StateAttention {
				blocked = true
			}
		}
		if blocked {
			continue
		}
		var msgs []Message
		for _, m := range s.newMessages(a, allMessages) {
			key := AgentKey{Server: ref.Server, MemberID: m.MidturnPeerSenderID}
			if fromOwner(m) || !m.Urgent || key.MemberID == "" || used[key] {
				continue
			}
			used[key] = true
			m.PeerBoundary = true
			msgs = append(msgs, m)
		}
		if len(msgs) != 0 {
			owners = append(owners, offer{agent: ref, mode: ModeAll, msgs: msgs})
		}
	}
	return owners
}

func (s *session) receivePeerBoundary(ctx context.Context, req Request) Response {
	h := s.handoffs[req.HandoffID]
	if h == nil || h.manifest.Class != ClassMidturnPeer || h.manifest.Boot != s.boot || req.Boot == "" || req.Boot != s.boot || req.TurnID != s.peerTurn || h.manifest.PeerTurn != s.peerTurn {
		return errorResponse("invalid_request", "This peer context receipt does not match the active turn.", "Leave it queued for turn end; do not retry with another turn's identity.")
	}
	confirmed, err := s.confirmHandoff(ctx, h)
	if err != nil {
		return errorResponse("internal", "Couldn't save the peer context receipt.", "The unconfirmed allocation stays available for recovery.")
	}
	s.logConfirmed(confirmed)
	for _, a := range s.agents {
		s.maybeAck(a)
	}
	return Response{V: ProtocolVersion}
}

func (s *session) requeueUnreceivedPeers(ctx context.Context) {
	for _, h := range s.handoffs {
		if h.manifest.Class != ClassMidturnPeer {
			continue
		}
		for _, part := range h.manifest.Parts {
			if a := s.agents[part.Agent.Key()]; a != nil {
				if dl := a.deliveries[part.DeliveryID]; dl != nil && dl.HandoffID == h.manifest.ID && dl.State == StateHanded {
					s.setState(ctx, dl, StatePending)
				}
			}
		}
	}
}
