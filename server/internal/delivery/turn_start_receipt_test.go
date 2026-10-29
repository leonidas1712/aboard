package delivery_test

import (
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestTurnStartNeedsReceiptBeforeAcknowledgingMessages(t *testing.T) {
	r := newRig(t)
	a := reviewer
	a.MemberID = "mem_prompt_receipt"
	r.register("s1", "b1")
	r.bind("claude-code", "s1", a)
	h := r.wait("s1", "b1", false)
	seq := r.broadcast(a, "keep this until printed")
	h.nothingFor("quiet receipt message")
	req := delivery.Request{Op: delivery.OpTurnStartReceipted, Harness: "claude-code", Session: "s1", Boot: "b1"}
	first := r.ok(req)
	if first.HandoffID == "" || first.Boot != "b1" || !strings.Contains(first.Bundle, "keep this until printed") {
		t.Fatalf("missing receipted bundle: %+v", first)
	}
	// A following event cannot prove that the previous hook printed its response.
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if r.server.Cursor(a) != 0 {
		t.Fatal("unseen prompt bundle acknowledged by next event")
	}
	second := r.ok(req)
	if !strings.Contains(second.Bundle, "keep this until printed") || second.HandoffID == "" {
		t.Fatalf("unreceived bundle not offered again: %+v", second)
	}
	if r.server.Cursor(a) != 0 {
		t.Fatal("unreceived bundle acknowledged by next prompt")
	}
	bad := r.call(delivery.Request{Op: delivery.OpReceived, Harness: "claude-code", Session: "s1", Boot: "wrong", HandoffID: second.HandoffID})
	if bad.Error == nil || r.server.Cursor(a) != 0 {
		t.Fatal("wrong boot receipt accepted")
	}
	r.ok(delivery.Request{Op: delivery.OpReceived, Harness: "claude-code", Session: "s1", Boot: second.Boot, HandoffID: second.HandoffID})
	r.eventually("receipted prompt acknowledged", 0, func() bool { return r.server.Cursor(a) == seq })
}

func TestReceiptedTurnStartDoesNotUseLegacyIdentityFallback(t *testing.T) {
	r := newRig(t)
	a := reviewer
	a.MemberID = ""
	r.register("s1", "b1")
	r.bind("claude-code", "s1", a)
	h := r.wait("s1", "b1", false)
	r.broadcast(a, "unverified seat")
	h.nothingFor("quiet legacy seat")
	got := r.ok(delivery.Request{Op: delivery.OpTurnStartReceipted, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if strings.Contains(got.Bundle, "unverified seat") || got.HandoffID != "" || r.server.Cursor(a) != 0 {
		t.Fatalf("unsafe legacy fallback: %+v", got)
	}
}
