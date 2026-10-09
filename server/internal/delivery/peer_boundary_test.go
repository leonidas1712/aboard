package delivery_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type peerPolicyServer struct {
	delivery.Server
	policy atomic.Pointer[string]
}

func (p *peerPolicyServer) Inbox(ctx context.Context, ref delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	msgs, cursor, mode, err = p.Server.Inbox(ctx, ref)
	if mode == nil {
		mode = &delivery.HeldMode{Mode: delivery.ModeFocused}
	} else {
		modeCopy := *mode
		mode = &modeCopy
	}
	if policy := p.policy.Load(); policy != nil {
		mode.MidturnPolicy = *policy
	}
	return msgs, cursor, mode, err
}

func TestPeerBoundaryUsesFreshPolicyDurableCapAndExplicitReceipt(t *testing.T) {
	var server *peerPolicyServer
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		server = &peerPolicyServer{Server: s}
		policy := "my-agents"
		server.policy.Store(&policy)
		return server
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "peer-boundary", Boot: "b1", Capabilities: []string{"tool-boundary", "midturn-peer"}}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "peer-boundary", reviewer)
	r.ok(req(delivery.OpPrompt))
	stale := req(delivery.OpBoundary)
	stale.Boot = "old-boot"
	if r.call(stale).Error == nil {
		t.Fatal("an older boot was allowed to rotate a peer-capable boundary")
	}
	post := func(id, sender, body string) int {
		return r.server.Post(reviewer, delivery.Message{ID: id, BoardID: "brd_docs", Urgent: true, MidturnPeerSenderID: sender, Sender: "owner_agent", FromName: "peer", To: []string{"@" + reviewer.Name}, Body: body})
	}
	first := post("msg_peer_first", "mem_peer", "first peer aside")
	got := r.ok(req(delivery.OpBoundary))
	if !strings.Contains(got.Bundle, "first peer aside") || !strings.Contains(got.Bundle, `delivery="tool-boundary"`) || got.HandoffID == "" {
		t.Fatalf("peer was not admitted at the boundary: %+v", got)
	}
	post("msg_peer_second", "mem_peer", "same sender excess")
	if next := r.ok(req(delivery.OpBoundary)); strings.Contains(next.Bundle, "same sender excess") {
		t.Fatal("a later tool call replenished the sender cap")
	}
	if r.server.Cursor(reviewer) >= first {
		t.Fatal("another tool event confirmed unreceived context")
	}
	ack := req(delivery.OpReceived)
	ack.HandoffID = got.HandoffID
	ack.TurnID, ack.Boot = got.TurnID, got.Boot
	r.ok(ack)
	r.eventually("explicit peer context receipt", 0, func() bool { return r.server.Cursor(reviewer) >= first })
	r.stop()
	r.start()
	r.ok(req(delivery.OpRegister))
	if next := r.ok(req(delivery.OpBoundary)); strings.Contains(next.Bundle, "same sender excess") {
		t.Fatal("daemon restart replenished the sender cap")
	}
	policy := "owner-only"
	server.policy.Store(&policy)
	post("msg_other_peer", "mem_other_peer", "policy downgrade refusal")
	if next := r.ok(req(delivery.OpBoundary)); strings.Contains(next.Bundle, "policy downgrade refusal") {
		t.Fatal("fresh policy downgrade was ignored")
	}
}

func TestUnreceivedPeerContextRecoversAtStopWithoutRenewingCap(t *testing.T) {
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		p := &peerPolicyServer{Server: s}
		policy := "my-agents"
		p.policy.Store(&policy)
		return p
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "peer-recovery", Boot: "b1", Capabilities: []string{"tool-boundary", "midturn-peer"}}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "peer-recovery", reviewer)
	r.ok(req(delivery.OpPrompt))
	first := r.server.Post(reviewer, delivery.Message{ID: "msg_unreceived", BoardID: "brd_docs", Urgent: true, MidturnPeerSenderID: "mem_peer", Sender: "owner_agent", Body: "unreceived context recovers"})
	got := r.ok(req(delivery.OpBoundary))
	if !strings.Contains(got.Bundle, "unreceived context recovers") || got.HandoffID == "" {
		t.Fatalf("missing first allocation: %+v", got)
	}
	r.ok(req(delivery.OpBoundary))
	if r.server.Cursor(reviewer) >= first {
		t.Fatal("failed context output was implicitly confirmed")
	}
	h := r.wait("peer-recovery", "b1", false, "codex")
	if text := h.bundle(); !strings.Contains(text, "unreceived context recovers") {
		t.Fatalf("unreceived context was lost at Stop: %q", text)
	}
	r.ok(req(delivery.OpPrompt))
	r.server.Post(reviewer, delivery.Message{ID: "msg_after_continuation", BoardID: "brd_docs", Urgent: true, MidturnPeerSenderID: "mem_peer", Sender: "owner_agent", Body: "continuation must not refill"})
	if text := r.ok(req(delivery.OpBoundary)).Bundle; strings.Contains(text, "continuation must not refill") {
		t.Fatal("Stop continuation replenished the sender allowance")
	}
}
