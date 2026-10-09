package delivery

import "context"

// ShownRecord keeps only exact identities, scoped to the emitting session and seat.
type ShownRecord struct {
	Session    SessionKey
	Boot       string
	Agent      AgentRef
	Generation uint64
	Message    ShownMessage
}

// ShownJournal retains read observations without storing text or moving cursors.
type ShownJournal interface {
	SaveShown(context.Context, []ShownRecord) error
	ShownMessages(context.Context, SessionKey, string) ([]ShownRecord, error)
}

func (s *session) ensureShown(ctx context.Context) bool {
	if s.shownLoaded && s.shownBoot == s.boot {
		return true
	}
	j, ok := s.d.cfg.Journal.(ShownJournal)
	if !ok {
		return true
	}
	observations, err := j.ShownMessages(ctx, s.key, s.boot)
	if err != nil {
		s.d.log.Warn("read shown-message observations", "error", err)
		return false
	}
	s.shown, s.shownBoot, s.shownLoaded = observations, s.boot, true
	return true
}

func (s *session) alreadyShown(a *agentState, m Message) bool {
	if s.shownBoot != s.boot {
		return false
	}
	for _, o := range s.shown {
		if o.Generation == a.generation && o.Agent.Key() == a.ref.Key() && o.Message.BoardID == m.BoardID && o.Message.MessageID == m.ID && o.Message.Seq == m.Seq {
			return true
		}
	}
	return false
}

func (s *session) observeShown(ctx context.Context, req Request) Response {
	if !s.open || req.Boot == "" || req.Boot != s.boot || req.Agent == nil || req.Agent.MemberID == "" || len(req.ShownMessages) == 0 || len(req.ShownMessages) > 1000 {
		return errorResponse("invalid_request", "This read observation does not match a live seat and boot.", "Keep the read result; only the current session may suppress its own automatic delivery.")
	}
	a := s.agents[req.Agent.Key()]
	if a == nil || a.adopting || a.gone() || a.generation == 0 || req.Generation != a.generation || a.ref.Server != req.Agent.Server || a.ref.MemberID != req.Agent.MemberID {
		return errorResponse("agent_not_selected", "That seat is not current in this session.", "Read with this session's own seat.")
	}
	j, ok := s.d.cfg.Journal.(ShownJournal)
	if !ok || !s.ensureShown(ctx) {
		return queueUnknown()
	}
	reader, ok := s.d.server(a.ref.Server).srv.(QueuedMessageServer)
	if !ok {
		return queueUnknown()
	}
	out := make([]ShownRecord, 0, len(req.ShownMessages))
	seen := map[string]bool{}
	for _, id := range req.ShownMessages {
		if id.BoardID == "" || id.MemberID != a.ref.MemberID || id.MessageID == "" || id.Seq <= 0 || seen[id.MessageID] {
			return errorResponse("invalid_request", "A shown identity is missing or repeated.", "Report only exact messages fully emitted by this read.")
		}
		seen[id.MessageID] = true
		readCtx, cancel := context.WithTimeout(ctx, recheckTimeout)
		m, err := reader.QueuedMessage(readCtx, a.ref, id.MessageID, id.Seq)
		cancel()
		if err != nil || m.ID != id.MessageID || m.Seq != id.Seq || m.BoardID != id.BoardID {
			return queueUnknown()
		}
		out = append(out, ShownRecord{Session: s.key, Boot: s.boot, Agent: a.ref, Generation: a.generation, Message: id})
	}
	if err := j.SaveShown(ctx, out); err != nil {
		return queueUnknown()
	}
	s.shown = append(s.shown, out...)
	return Response{V: ProtocolVersion, Shown: true}
}

func (s *session) deliveryFullyShown(a *agentState, dl *Delivery) bool {
	if len(dl.Seqs) == 0 || s.shownBoot != s.boot {
		return false
	}
	for _, seq := range dl.Seqs {
		found := false
		for _, o := range s.shown {
			if o.Agent.Key() == a.ref.Key() && o.Generation == a.generation && o.Message.Seq == seq {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
