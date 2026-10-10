package delivery_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestApprovalWatchRejectsUnboundSessionBeforeReading(t *testing.T) {
	r, _ := seatsRig(t)
	r.register("s1", "b1")
	got := r.call(delivery.Request{Op: "approval_watch", Harness: "claude-code", Session: "s1", Server: serverURL, Agent: &delivery.AgentRef{Server: serverURL, Board: "docs", Name: "claude", MemberID: "mem_not_bound"}})
	if got.Error == nil || got.Error.Code != "agent_not_selected" {
		t.Fatalf("unbound watch: %+v", got)
	}
}

type approvalReader struct {
	mu       sync.Mutex
	decision delivery.ApprovalDecision
	err      error
	reads    []delivery.AgentRef
}

func (a *approvalReader) Get(_ context.Context, _ string, seat delivery.AgentRef) (delivery.ApprovalDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads = append(a.reads, seat)
	return a.decision, a.err
}

func (a *approvalReader) set(state, pairing string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decision.State = state
	a.decision.PairingRequestID = pairing
	a.err = err
}

func approvalFixture(t *testing.T) (*rig, *approvalReader, delivery.AgentRef) {
	t.Helper()
	reader := &approvalReader{decision: delivery.ApprovalDecision{ID: "apr_own", AgentID: "mem_own", State: "pending"}}
	r, _ := seatsRigWith(t, func(c *delivery.Config) { c.ApprovalFor = func(string) delivery.ApprovalRuntime { return reader } })
	r.register("s1", "b1")
	agent := delivery.AgentRef{Server: serverURL, Board: "docs", Name: "claude", MemberID: "mem_own"}
	r.bind("claude-code", "s1", agent)
	r.ok(delivery.Request{Op: delivery.OpApprovalWatch, Harness: "claude-code", Session: "s1", Server: serverURL, ApprovalID: "apr_own", Agent: &agent})
	return r, reader, agent
}

func approvalTurn(r *rig, session, boot string) delivery.Response {
	return r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: session, Boot: boot})
}

func TestApprovalDecisionReachesOriginalNextTurnAfterRestart(t *testing.T) {
	r, reader, _ := approvalFixture(t)
	if got := approvalTurn(r, "s1", "b1"); strings.Contains(got.Nudge, "approval apr_own") {
		t.Fatal("pending approval notified")
	}
	r.restart()
	reader.set("executed", "", nil)
	got := approvalTurn(r, "s1", "b1")
	if !strings.Contains(got.Nudge, "aboard approvals show 'apr_own' --server '"+serverURL+"' --board 'docs' --json") {
		t.Fatalf("no actual outcome command: %+v", got)
	}
	if got := approvalTurn(r, "s1", "b1"); !strings.Contains(got.Nudge, "approval apr_own") {
		t.Fatal("unshown Nudge consumed the pending notice")
	}
}

func TestApprovalFailedFreshReadRemainsRecoverable(t *testing.T) {
	r, reader, _ := approvalFixture(t)
	reader.set("executed", "", errors.New("network unavailable"))
	if got := approvalTurn(r, "s1", "b1"); strings.Contains(got.Nudge, "approval apr_own") {
		t.Fatal("failed read produced notice")
	}
	reader.set("declined", "", nil)
	if got := approvalTurn(r, "s1", "b1"); !strings.Contains(got.Nudge, "was declined") {
		t.Fatalf("lost decision: %+v", got)
	}
}

func TestApprovalNoticeRetainsItsOriginalAuthorityAfterResume(t *testing.T) {
	for _, change := range []string{"rebind", "boot", "incoming_boot", "closed"} {
		t.Run(change, func(t *testing.T) {
			r, reader, agent := approvalFixture(t)
			reader.set("executed", "prq_invite", nil)
			session, boot := "s1", "b1"
			switch change {
			case "rebind":
				r.register("s2", "b2")
				r.bind("claude-code", "s2", agent)
				session, boot = "s2", "b2"
			case "boot":
				r.register("s1", "new")
				boot = "new"
			case "incoming_boot":
				boot = "new"
			case "closed":
				r.ok(delivery.Request{Op: delivery.OpEnd, Harness: "claude-code", Session: "s1", Boot: "b1"})
			}
			_ = approvalTurn(r, session, boot)
			origins, err := r.journal.ApprovalSeatWatches(context.Background(), agent)
			if err != nil || len(origins) != 1 || origins[0].Session.ID != "s1" || origins[0].Boot != "b1" {
				t.Fatal("notice transferred original authority", origins, err)
			}
		})
	}
}

func TestApprovalWatchRefusesAnotherRequestingSeat(t *testing.T) {
	r, reader, agent := approvalFixture(t)
	reader.mu.Lock()
	reader.decision.AgentID = "mem_other"
	reader.mu.Unlock()
	got := r.call(delivery.Request{Op: delivery.OpApprovalWatch, Harness: "claude-code", Session: "s1", Server: serverURL, ApprovalID: "apr_own", Agent: &agent})
	if got.Error == nil || got.Error.Code != "forbidden" {
		t.Fatalf("foreign approval accepted: %+v", got)
	}
}

type approvalPairing struct {
	readyPairingRuntime
	selectedAgent delivery.AgentRef
	side          string
}

func (p *approvalPairing) Select(_ context.Context, row delivery.PairingRequest, side string, agent delivery.AgentRef, binding string, replace bool, _ string) (delivery.PairingRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	p.selectedAgent = agent
	p.side = side
	if replace {
		return row, errors.New("must not replace")
	}
	p.row.Initiator = &delivery.PairingEndpoint{AgentID: agent.MemberID, SessionBinding: binding, Generation: row.Generation}
	p.row.State = "verifying"
	return p.row, nil
}

func TestApprovalAutomaticallySelectsOnlyItsOriginatingPairing(t *testing.T) {
	for _, mode := range []string{"original", "new_boot", "manual_endpoint", "foreign_agent"} {
		t.Run(mode, func(t *testing.T) {
			r, reader, agent := approvalFixture(t)
			pairing := &approvalPairing{readyPairingRuntime: readyPairingRuntime{row: delivery.PairingRequest{ID: "prq_invite", InitiatingAgentID: agent.MemberID, State: "awaiting_account", Generation: 1}}}
			old := r.configure
			r.configure = func(c *delivery.Config) {
				old(c)
				c.PairingFor = func(string) delivery.PairingRuntime { return pairing }
			}
			r.restart()
			boot := "b1"
			switch mode {
			case "new_boot":
				r.register("s1", "new")
				boot = "new"
			case "manual_endpoint":
				pairing.row.Initiator = &delivery.PairingEndpoint{AgentID: agent.MemberID, SessionBinding: "sha256:explicit_other_session"}
			case "foreign_agent":
				pairing.row.InitiatingAgentID = "mem_other"
			}
			reader.set("executed", "prq_invite", nil)
			got := approvalTurn(r, "s1", boot)
			pairing.mu.Lock()
			writes, side, selected := pairing.writes, pairing.side, pairing.selectedAgent
			pairing.mu.Unlock()
			if mode == "original" {
				if writes != 1 || side != "initiator" || selected.Key() != agent.Key() || !strings.Contains(got.Nudge, "Your invite was approved. Collect its link with the command above.") {
					t.Fatalf("origin not selected: %d %s %+v %+v", writes, side, selected, got)
				}
			} else if writes != 0 || strings.Contains(got.Nudge, "Your invite was approved. Collect its link with the command above.") {
				t.Fatalf("selected wrong endpoint: %d %+v", writes, got)
			}
		})
	}
}
