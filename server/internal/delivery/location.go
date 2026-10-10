package delivery

import (
	"context"
	"path/filepath"
)

// SessionLocation is descriptive runtime bookkeeping, never identity evidence.
type SessionLocation struct {
	Harness   string
	SessionID string
	Folder    string
}

// LocationReporter reports only the selected seat through its own credential.
// Older server adapters can omit it.
type LocationReporter interface {
	ReportLocation(context.Context, AgentRef, SessionLocation) error
}

func (s *session) noteLocation(ctx context.Context, req Request) {
	if req.Subagent != "" {
		return
	}
	oldFolder, oldBoot := s.folder, s.locationBoot
	switch req.Op {
	case OpRegister, OpHello:
		if s.locationBoot != s.boot {
			s.folder = ""
		}
		s.locationBoot = s.boot
		if req.Cwd != "" && filepath.IsAbs(req.Cwd) {
			s.folder = req.Cwd
		}
	case OpPrompt, OpTurnStart, OpBoundary, OpUrgent:
		if req.Boot != "" && req.Boot != s.locationBoot {
			return
		}
		if req.Cwd != "" && filepath.IsAbs(req.Cwd) {
			s.folder = req.Cwd
		}
	case OpBind:
	default:
		return
	}
	changed := oldFolder != s.folder || oldBoot != s.locationBoot
	if changed {
		s.locationEpoch.Add(1)
		s.saveSession(ctx)
	}
	if changed || req.Op == OpBind || req.Op == OpRegister || req.Op == OpHello {
		s.publishLocation()
	}
}

func (s *session) publishLocation() {
	if !s.open || s.folder == "" {
		return
	}
	for _, a := range s.agents {
		if a.adopting || a.gone() {
			continue
		}
		ref := a.ref
		location := SessionLocation{Harness: s.key.Harness, SessionID: s.key.ID, Folder: s.folder}
		s.d.server(ref.Server).mail.put(srvMsg{locationAgent: &ref, location: &location, generation: a.generation, locationOwner: s, locationEpoch: s.locationEpoch.Load()})
	}
}

func (c *serverConn) reportLocations(ctx context.Context, batch []srvMsg) {
	reporter, ok := c.srv.(LocationReporter)
	if !ok {
		return
	}
	latest := map[AgentKey]srvMsg{}
	for _, m := range batch {
		if m.locationAgent != nil {
			latest[m.locationAgent.Key()] = m
		}
	}
	for _, m := range latest {
		agent := *m.locationAgent
		if c.gone[agent.Key()] || m.generation != c.d.generation(agent) || m.locationOwner == nil || c.d.owner(agent) != m.locationOwner || m.locationEpoch != m.locationOwner.locationEpoch.Load() {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, serverRequestTimeout)
		err := reporter.ReportLocation(rctx, agent, *m.location)
		cancel()
		if err != nil {
			c.d.log.Warn("report session location", "agent", agent.Name, "board", agent.Board, "error", err)
		}
	}
}
