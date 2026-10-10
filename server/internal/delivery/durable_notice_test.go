package delivery_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestTerminalApprovalUsesExistingIdleHookTransport(t *testing.T) {
	r, reader, agent := approvalFixture(t)
	reader.set("executed", "", nil)
	h := r.wait("s1", "b1", false)
	got := h.next()
	if !strings.Contains(got.Bundle, "aboard approvals show 'apr_own' --server '"+serverURL+"' --board 'docs' --json") || got.HandoffID != "" {
		t.Fatal("notice did not use ordinary handoff", got)
	}
	r.eventually("notice handed", 0, func() bool {
		rows, _ := r.journal.Notices(context.Background(), agent)
		return len(rows) == 1 && rows[0].Handed
	})
	manifests, err := r.journal.Handoffs(context.Background())
	if err != nil || len(manifests) != 0 {
		t.Fatal("notice created proof manifest", manifests, err)
	}
}

func TestApprovalNudgeAndCursorNeverConsumePendingNotice(t *testing.T) {
	r, reader, agent := approvalFixture(t)
	reader.set("declined", "", nil)
	got := approvalTurn(r, "s1", "b1")
	if !strings.Contains(got.Nudge, "was declined") || got.HandoffID != "" {
		t.Fatal(got)
	}
	_ = r.server.Ack(context.Background(), agent, 100)
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "s1", Boot: "b1"})
	rows, _ := r.journal.Notices(context.Background(), agent)
	if len(rows) != 1 || rows[0].Handed {
		t.Fatal("unshown Nudge or cursor consumed notice", rows)
	}
}

func TestClosedApprovalOriginNotifiesOnlyExplicitResumedSeat(t *testing.T) {
	r, reader, agent := approvalFixture(t)
	r.ok(delivery.Request{Op: delivery.OpEnd, Harness: "claude-code", Session: "s1", Boot: "b1"})
	reader.set("executed", "", nil)
	r.register("unrelated", "b2")
	r.register("resumed", "b3")
	if got := approvalTurn(r, "unrelated", "b2"); strings.Contains(got.Nudge, "apr_own") {
		t.Fatal("notice guessed another session", got)
	}
	r.bind("claude-code", "resumed", agent)
	r.eventually("resumed binding adopted", 0, func() bool {
		got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "resumed", Boot: "b3"})
		return len(got.Agents) == 1
	})
	got := r.wait("resumed", "b3", false).next()
	if !strings.Contains(got.Bundle, "was executed") {
		t.Fatal(got)
	}
	origins, _ := r.journal.ApprovalSeatWatches(context.Background(), agent)
	if len(origins) != 1 || origins[0].Session.ID != "s1" || origins[0].Boot != "b1" {
		t.Fatal("origin authority transferred", origins)
	}
}

func TestFreshModeOffKeepsAnApprovalNoticePending(t *testing.T) {
	reader := &approvalReader{decision: delivery.ApprovalDecision{ID: "apr_own", AgentID: "mem_own", State: "pending"}}
	r, _ := seatsRigWith(t, func(c *delivery.Config) { c.ApprovalFor = func(string) delivery.ApprovalRuntime { return reader } })
	gate := &heldInbox{Server: r.server, reading: make(chan struct{}), release: make(chan struct{})}
	r.remote = gate
	r.server.HoldModes()
	r.register("s1", "b1")
	agent := delivery.AgentRef{Server: serverURL, Board: "docs", Name: "claude", MemberID: "mem_own"}
	r.bind("claude-code", "s1", agent)
	r.ok(delivery.Request{Op: delivery.OpApprovalWatch, Harness: "claude-code", Session: "s1", Server: serverURL, ApprovalID: "apr_own", Agent: &agent})
	r.ok(delivery.Request{Op: delivery.OpQueued, Harness: "claude-code", Session: "s1", Server: serverURL})
	reader.set("declined", "", nil)
	gate.armed.Store(true)
	result := make(chan delivery.Response, 1)
	go func() {
		result <- r.call(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s1", Boot: "b1"})
	}()
	select {
	case <-gate.reading:
	case <-time.After(within):
		t.Fatal("fresh mode read did not begin")
	}
	r.server.SetHeldMode(agent, delivery.ModeOff)
	close(gate.release)
	var got delivery.Response
	select {
	case got = <-result:
	case <-time.After(within):
		t.Fatal("turn read did not complete")
	}
	if strings.Contains(got.Nudge, "was declined") {
		t.Fatal("fresh off mode was bypassed", got)
	}
	notices, _ := r.journal.Notices(context.Background(), agent)
	if len(notices) != 1 || notices[0].Handed || notices[0].Cancelled {
		t.Fatal("off mode lost pending notice", notices)
	}
	r.server.SetHeldMode(agent, delivery.ModeFocused)
	r.ok(delivery.Request{Op: delivery.OpQueued, Harness: "claude-code", Session: "s1", Server: serverURL})
	if got := approvalTurn(r, "s1", "b1"); !strings.Contains(got.Nudge, "was declined") {
		t.Fatal("pending notice did not recover", got)
	}
}

func TestArrivalNeedsRedeemedAccountNotChosenEndpoint(t *testing.T) {
	pairing := &readyPairingRuntime{row: delivery.PairingRequest{ID: "prq_own", BoardID: "brd_docs", InitiatingAgentID: "mem_own", State: "awaiting_account", Display: []byte(`{"recipient_handle":"pat"}`)}}
	r, _ := seatsRigWith(t, func(c *delivery.Config) { c.PairingFor = func(string) delivery.PairingRuntime { return pairing } })
	r.register("s1", "b1")
	agent := delivery.AgentRef{Server: serverURL, Board: "docs", Name: "claude", MemberID: "mem_own"}
	r.bind("claude-code", "s1", agent)
	bindings, err := r.journal.Bindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gen uint64
	for _, b := range bindings {
		if b.Agent.Key() == agent.Key() {
			gen = b.Generation
		}
	}
	n := delivery.DurableNotice{ID: "arrival", Kind: "colleague_arrival", SourceID: "prq_own", BoardID: "brd_docs", Agent: agent, Session: delivery.SessionKey{Harness: "claude-code", ID: "s1"}, Boot: "b1", Generation: gen}
	if err := r.journal.SaveNotice(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if got := approvalTurn(r, "s1", "b1"); strings.Contains(got.Nudge, "joined board") {
		t.Fatal("unredeemed account reported arrival", got)
	}
	pairing.mu.Lock()
	pairing.row.RecipientID = "hum_pat"
	pairing.row.State = "awaiting_session"
	pairing.mu.Unlock()
	got := approvalTurn(r, "s1", "b1")
	if !strings.Contains(got.Nudge, "pat joined board docs") || !strings.Contains(got.Nudge, "Say hello to them on this board.") {
		t.Fatal(got)
	}
	pairing.mu.Lock()
	defer pairing.mu.Unlock()
	if pairing.writes != 0 {
		t.Fatal("notice selected an endpoint")
	}
}

func TestCodexNoticeQueueAdmissionIsHandedNotAProof(t *testing.T) {
	reader := &approvalReader{decision: delivery.ApprovalDecision{ID: "apr_own", AgentID: "mem_own", State: "pending"}}
	r, _ := seatsRigWith(t, func(c *delivery.Config) { c.ApprovalFor = func(string) delivery.ApprovalRuntime { return reader } })
	req := delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "notice-queue", Boot: "b1"}
	r.ok(req)
	agent := delivery.AgentRef{Server: serverURL, Board: "docs", Name: "codex", MemberID: "mem_own"}
	r.bind("codex", req.Session, agent)
	watch := req
	watch.Op, watch.Server, watch.ApprovalID, watch.Agent = delivery.OpApprovalWatch, serverURL, "apr_own", &agent
	r.ok(watch)
	r.codex.SetBusy(true)
	reader.set("executed", "", nil)
	r.eventually("busy notice attempt", time.Second, func() bool { return r.codex.Attempts() > 0 })
	pending, _ := r.journal.Notices(context.Background(), agent)
	if len(pending) != 1 || pending[0].Handed {
		t.Fatal("refused transport consumed notice", pending)
	}
	r.codex.SetBusy(false)
	r.eventually("queue handoff", time.Second, func() bool {
		rows, _ := r.journal.Notices(context.Background(), agent)
		return len(rows) == 1 && rows[0].Handed
	})
	for range 3 {
		r.clock.Advance(time.Second)
		req.Op = delivery.OpAgents
		r.ok(req)
	}
	if len(r.codex.Handed(req.Session)) != 1 {
		t.Fatal("notice queued repeatedly")
	}
	manifests, err := r.journal.Handoffs(context.Background())
	if err != nil || len(manifests) != 0 {
		t.Fatal("queue admission became proof", manifests, err)
	}
}

func TestNoticeUsesStableNonzeroLegacyIDAcrossExtensionReconnect(t *testing.T) {
	reader := &approvalReader{decision: delivery.ApprovalDecision{ID: "apr_own", AgentID: "mem_own", State: "pending"}}
	r, _ := seatsRigWith(t, func(c *delivery.Config) { c.ApprovalFor = func(string) delivery.ApprovalRuntime { return reader } })
	hello := delivery.Request{Harness: "omp", Session: "notice-wire", Boot: "b1", Source: "startup", Process: &delivery.Process{PID: 51234}}
	e, welcome := r.connect(hello)
	if welcome.Event != delivery.EventWelcome {
		t.Fatal(welcome)
	}
	agent := delivery.AgentRef{Server: serverURL, Board: "docs", Name: "omp", MemberID: "mem_own"}
	r.bind("omp", hello.Session, agent)
	r.ok(delivery.Request{Op: delivery.OpApprovalWatch, Harness: "omp", Session: hello.Session, Server: serverURL, ApprovalID: "apr_own", Agent: &agent})
	reader.set("executed", "", nil)
	first := e.next()
	if first.Event != delivery.EventDeliver || first.ID >= 0 || first.ID < -9007199254740991 || first.HandoffID != "" {
		t.Fatal("notice has no safe existing transport id", first)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["id"]; !ok {
		t.Fatal("wire omitted transport id")
	}
	// Losing this connection before receipt must leave the notice pending.
	if err := e.conn.Close(); err != nil {
		t.Fatal(err)
	}
	e, welcome = r.connect(hello)
	if welcome.Event != delivery.EventWelcome {
		t.Fatal(welcome)
	}
	next := e.next()
	if next.ID != first.ID || next.Bundle != first.Bundle {
		t.Fatal("retry changed transport identity", first, next)
	}
	rows, _ := r.journal.Notices(context.Background(), agent)
	if len(rows) != 1 || rows[0].Handed {
		t.Fatal("disconnect counted as acceptance", rows)
	}
	e.send(delivery.Request{Op: delivery.OpReceived, ID: next.ID})
	r.eventually("existing legacy receipt", 0, func() bool {
		rows, _ := r.journal.Notices(context.Background(), agent)
		return len(rows) == 1 && rows[0].Handed
	})
	manifests, err := r.journal.Handoffs(context.Background())
	if err != nil || len(manifests) != 0 {
		t.Fatal("notice created proof manifest", manifests, err)
	}
}
