package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSessionRetainsSeatsAndAcknowledgesEqualBoardSequences(t *testing.T) {
	r := newRig(t)
	a, b := reviewer, planner
	a.MemberID, b.MemberID = "mem_docs", "mem_plans"
	r.bind("codex", "both", a)
	r.bind("codex", "both", b)
	refs := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "both"}).Agents
	if len(refs) != 2 {
		t.Fatalf("second board displaced first seat: %+v", refs)
	}
	first := r.postFromOwner(a, "docs decision")
	second := r.postFromOwner(b, "plans decision")
	if first != second {
		t.Fatalf("fixture must have equal board-local sequences: %d/%d", first, second)
	}
	r.eventually("one combined handoff", 500*time.Millisecond, func() bool { return len(r.codex.Handed("both")) == 1 })
	text := r.codex.Handed("both")[0]
	for _, want := range []string{"docs decision", "plans decision", "--board docs", "--board plans"} {
		if !strings.Contains(text, want) {
			t.Errorf("combined payload omits %q", want)
		}
	}
	r.eventually("independent acknowledgments", 0, func() bool { return r.server.Cursor(a) == first && r.server.Cursor(b) == second })
	r.restart()
	refs = r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "both"}).Agents
	if len(refs) != 2 {
		t.Fatalf("restart lost seat: %+v", refs)
	}
	if len(r.codex.Handed("both")) != 1 {
		t.Fatal("restart repeated confirmed payload")
	}
}

func TestAddingSeatOnAnotherServerRefusesWithoutChangingBinding(t *testing.T) {
	r := newRig(t)
	a, b := reviewer, planner
	a.MemberID, b.MemberID = "mem_docs", "mem_other"
	b.Server = "https://other.invalid"
	r.bind("codex", "one-server", a)
	got := r.call(delivery.Request{Op: delivery.OpBind, Harness: "codex", Session: "one-server", Agent: &b})
	if got.Error == nil || got.Error.Code != "session_on_another_server" {
		t.Fatalf("cross-server bind: %+v", got)
	}
	refs := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "one-server"}).Agents
	if len(refs) != 1 || refs[0].MemberID != a.MemberID {
		t.Fatalf("refused bind changed original seat: %+v", refs)
	}
}
