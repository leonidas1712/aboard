package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ApprovalDecision contains only metadata readable by the requesting seat.
type ApprovalDecision struct {
	ID, AgentID, State, PairingRequestID string
}

// ApprovalRuntime reads a decision with the exact requesting seat's credential.
// It deliberately has no collection method.
type ApprovalRuntime interface {
	Get(context.Context, string, AgentRef) (ApprovalDecision, error)
}

// ApprovalWatch remembers an originating session, not a recently active session.
type ApprovalWatch struct {
	ID         string
	BoardID    string
	Agent      AgentRef
	Session    SessionKey
	Boot       string
	Generation uint64
	Notified   bool
}

// ApprovalJournal retains watches across a daemon restart and fences their writes.
type ApprovalJournal interface {
	SaveApprovalWatch(context.Context, ApprovalWatch) error
	ApprovalWatches(context.Context, SessionKey) ([]ApprovalWatch, error)
}

func (s *session) watchApproval(ctx context.Context, req Request) Response {
	if req.Agent == nil || req.Agent.Server != req.Server || req.Agent.MemberID == "" || req.Subagent != "" || (req.Boot != "" && req.Boot != s.boot) {
		return errorResponse("agent_not_selected", "Approval watches need this session's exact requesting seat.", "Use the session that requested the approval.")
	}
	a := s.agents[req.Agent.Key()]
	if a == nil || a.adopting || a.gone() || !s.open || a.ref.Board != req.Agent.Board {
		return errorResponse("agent_not_selected", "The requesting seat is not in this exact session.", "Resume the requesting seat in its original session.")
	}
	if req.ApprovalID == "" || s.boot == "" || s.d.cfg.ApprovalFor == nil {
		return errorResponse("invalid_request", "An approval watch needs an id and an open runtime.", "Restart an up-to-date daemon and retry the requesting command.")
	}
	j, ok := s.d.cfg.Journal.(ApprovalJournal)
	if !ok {
		return errorResponse("internal", "Approval watches are unavailable.", "Restart an up-to-date daemon.")
	}
	runtime := s.d.cfg.ApprovalFor(req.Server)
	if runtime == nil {
		return errorResponse("internal", "Approval reads are unavailable.", "Restart an up-to-date daemon.")
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	decision, err := runtime.Get(rctx, req.ApprovalID, a.ref)
	if err != nil {
		return seatsError(req.Server, err)
	}
	if decision.ID != req.ApprovalID || decision.AgentID != a.ref.MemberID {
		return errorResponse("forbidden", "This approval belongs to another requesting seat.", "Use the seat that requested it.")
	}
	w, err := s.approvalWatch(ctx, req.ApprovalID, a.ref)
	if err == nil {
		w.BoardID = req.BoardID
		err = j.SaveApprovalWatch(ctx, w)
	}
	if err != nil {
		return errorResponse("pairing_changed", "The originating seat changed sessions.", "Use aboard approvals show with the requesting seat.")
	}
	return Response{V: ProtocolVersion}
}

func (s *session) approvalWatch(ctx context.Context, id string, agent AgentRef) (ApprovalWatch, error) {
	rows, err := s.d.cfg.Journal.Bindings(ctx)
	if err != nil {
		return ApprovalWatch{}, err
	}
	for _, row := range rows {
		if row.Agent.Key() == agent.Key() && row.Session == s.key {
			return ApprovalWatch{ID: id, Agent: agent, Session: s.key, Boot: s.boot, Generation: row.Generation}, nil
		}
	}
	return ApprovalWatch{}, fmt.Errorf("originating binding missing")
}

func (s *session) approvalNotices(ctx context.Context) string {
	j, ok := s.d.cfg.Journal.(ApprovalJournal)
	if !ok || s.d.cfg.ApprovalFor == nil || !s.open {
		return ""
	}
	rows, err := j.ApprovalWatches(ctx, s.key)
	if err != nil {
		return ""
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var notices []string
	for _, w := range rows {
		a := s.agents[w.Agent.Key()]
		if w.Notified || a == nil || a.adopting || a.gone() || w.Boot != s.boot {
			continue
		}
		current, err := s.approvalWatch(ctx, w.ID, a.ref)
		if err != nil || current.Generation != w.Generation {
			continue
		}
		runtime := s.d.cfg.ApprovalFor(w.Agent.Server)
		if runtime == nil {
			continue
		}
		decision, err := runtime.Get(rctx, w.ID, a.ref)
		if err != nil || decision.ID != w.ID || decision.AgentID != a.ref.MemberID {
			continue
		}
		switch decision.State {
		case "executed", "declined", "expired":
		default:
			continue
		}
		pairingComplete, pairingSelected := s.selectApprovalPairing(ctx, decision, w)
		command := "aboard approvals show " + shellWord(w.ID) + " --server " + shellWord(w.Agent.Server)
		notices = append(notices, fmt.Sprintf("Aboard approval %s on %s was %s. Run %s to read its outcome.", w.ID, w.Agent.Server, decision.State, command))
		if pairingSelected {
			notices = append(notices, "The requesting session is selected as the initiating pairing endpoint; ready still requires the delivery check.")
		}
		w.Notified = decision.PairingRequestID == "" || pairingComplete || decision.State != "executed"
		if err := j.SaveApprovalWatch(ctx, w); err != nil {
			s.d.log.Warn("save approval notice", "approval", w.ID)
		}
	}
	return strings.Join(notices, "\n")
}

func (s *session) selectApprovalPairing(ctx context.Context, decision ApprovalDecision, w ApprovalWatch) (complete, selected bool) {
	if decision.State != "executed" || decision.PairingRequestID == "" || s.d.cfg.PairingFor == nil {
		return false, false
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	runtime := s.d.cfg.PairingFor(w.Agent.Server)
	if runtime == nil {
		return false, false
	}
	row, err := runtime.Get(rctx, decision.PairingRequestID)
	if err != nil || row.ID != decision.PairingRequestID || row.InitiatingAgentID != w.Agent.MemberID || (w.BoardID != "" && row.BoardID != w.BoardID) {
		return false, false
	}
	if row.State == "declined" || row.State == "cancelled" || row.State == "expired" {
		return true, false
	}
	binding, err := s.d.pairingBinding(rctx, s.key, w.Agent)
	if err != nil {
		return false, false
	}
	// Never replace an endpoint that the person selected explicitly in another session.
	if row.Initiator != nil {
		if row.Initiator.AgentID != w.Agent.MemberID || row.Initiator.SessionBinding != binding {
			return true, false
		}
		s.d.watchPairing(ctx, s.key, runtime, row, w.Agent, binding)
		return true, true
	}
	if row.State == "ready" {
		return true, false
	}
	got, err := runtime.Select(rctx, row, "initiator", w.Agent, binding, false, "approval-"+w.ID)
	if err != nil {
		return false, false
	}
	s.d.watchPairing(ctx, s.key, runtime, got, w.Agent, binding)
	return true, true
}

func shellWord(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
