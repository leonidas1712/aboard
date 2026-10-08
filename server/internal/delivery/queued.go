package delivery

import (
	"cmp"
	"context"
	"slices"
)

func queuedIdentity(ref AgentRef, m Message) QueuedMessage {
	return QueuedMessage{BoardID: m.BoardID, MemberID: ref.MemberID, MessageID: m.ID, Seq: m.Seq, From: "@" + m.FromName, Boundary: "turn_end"}
}

func (s *session) queued(ctx context.Context, req Request) Response {
	if !s.open || (req.Boot != "" && req.Boot != s.boot) {
		return errorResponse("session_unknown", "The current session's queue cannot be observed.", "Use the current session; reconnect if it has ended.")
	}
	if !s.ensureShown(ctx) {
		return queueUnknown()
	}
	out := QueuedMessages{Messages: []QueuedMessage{}}
	seen := map[string]bool{}
	for _, ref := range s.agentRefs() {
		a := s.agents[ref.Key()]
		if a.adopting || a.problem != "" {
			return queueUnknown()
		}
		rctx, cancel := context.WithTimeout(ctx, recheckTimeout)
		srv := s.d.server(ref.Server).srv
		msgs, _, mode, err := srv.Inbox(rctx, ref)
		cancel()
		if err != nil {
			return queueUnknown()
		}
		accepted := map[int64]bool{}
		for _, dl := range s.awaitingTurn {
			if dl.Agent.Key() != ref.Key() {
				continue
			}
			if !dl.AcceptedAt.IsZero() && dl.TurnStartedAt.IsZero() && (dl.State == StateHanded || dl.State == StateConfirmed || dl.State == StateDone) {
				accepted[dl.ID] = true
			}
		}
		covered := map[int64]bool{}
		for _, h := range s.handoffs {
			if h.manifest.Boot != s.boot {
				continue
			}
			for _, part := range h.manifest.Parts {
				if part.Agent.Key() != ref.Key() || part.Generation != a.generation || !accepted[part.DeliveryID] {
					continue
				}
				if len(part.Messages) != len(part.Seqs) {
					return queueUnknown()
				}
				covered[part.DeliveryID] = true
				reader, ok := srv.(QueuedMessageServer)
				if !ok || mode == nil || mode.BoardID == "" {
					return queueUnknown()
				}
				for _, identity := range part.Messages {
					if identity.BoardID != mode.BoardID || identity.MemberID != ref.MemberID {
						return queueUnknown()
					}
					qctx, done := context.WithTimeout(ctx, recheckTimeout)
					m, readErr := reader.QueuedMessage(qctx, ref, identity.MessageID, identity.Seq)
					done()
					if readErr != nil {
						return queueUnknown()
					}
					identity.From = "@" + m.FromName
					key := ref.Server + "/" + identity.MessageID
					if !seen[key] {
						out.Messages = append(out.Messages, identity)
						seen[key] = true
					}
				}
			}
		}
		for id := range accepted {
			if !covered[id] {
				return queueUnknown()
			}
		}
		if !s.working && !s.inTurn {
			continue
		}
		if s.d.mode(ref) == ModeOff {
			continue
		}
		supported, known := s.peerBoundaryState()
		nextStep := s.peerNextCandidates(ref)
		if !known && len(nextStep) > 0 {
			return queueUnknown()
		}
		taken := taken(a)
		for _, m := range msgs {
			if fromOwner(m) || s.held(ref, m) || s.alreadyShown(a, m) || (supported && nextStep[m.ID]) {
				continue
			}
			if _, claimed := taken[m.Seq]; claimed {
				continue
			}
			if m.ID == "" || m.BoardID == "" || ref.MemberID == "" {
				return queueUnknown()
			}
			key := ref.Server + "/" + m.ID
			if !seen[key] {
				out.Messages = append(out.Messages, queuedIdentity(ref, m))
				seen[key] = true
			}
		}
	}
	slices.SortFunc(out.Messages, func(a, b QueuedMessage) int {
		if a.BoardID != b.BoardID {
			return cmp.Compare(a.BoardID, b.BoardID)
		}
		return a.Seq - b.Seq
	})
	out.Count = len(out.Messages)
	return Response{V: ProtocolVersion, Queued: &out, Agents: s.agentRefs()}
}

func queueUnknown() Response {
	return errorResponse("queue_unknown", "The current queue could not be verified.", "Check the server connection and current seat, then try again; nothing was acknowledged.")
}
