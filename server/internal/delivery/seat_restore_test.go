package delivery_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// seatProblem is the reason status gives for an agent, empty when it gives none.
func seatProblem(st *delivery.Status, agent delivery.AgentRef) string {
	for _, p := range st.Agents {
		if p.Agent.Key() == agent.Key() {
			return p.Reason
		}
	}
	return ""
}

// A daemon that starts while the seat's server is down (aboard down, then aboard status)
// can't prove an unverified journal seat yet. The outage is temporary: the daemon keeps
// trying, and once the server answers, the seat is restored and delivered to, with no
// restart of the daemon.
func TestUnreachableServerAtStartRestoresTheSeatOnceItAnswers(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	legacy := reviewer
	legacy.MemberID = ""
	if err := r.journal.Bind(context.Background(), delivery.Binding{Agent: legacy, Session: delivery.SessionKey{Harness: "claude-code", ID: "s1"}, BoundAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	r.stop()
	var up atomic.Bool
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		if !up.Load() {
			return delivery.AgentRef{}, errors.New("server_unreachable: connection refused")
		}
		ref.MemberID = reviewer.MemberID
		return ref, nil
	}
	r.start()
	if got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"}); len(got.Agents) != 0 {
		t.Fatalf("an unverified seat was restored while its server was down: %+v", got)
	}
	if p := seatProblem(r.status(), legacy); p != "server_unreachable" {
		t.Fatalf("a seat whose server is down shows %q, want server_unreachable", p)
	}
	// The daemon keeps trying while the server stays down.
	r.clock.Advance(10 * time.Second)

	up.Store(true)
	r.eventually("the restored seat", time.Second, func() bool {
		got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"})
		return len(got.Agents) == 1 && got.Agents[0] == reviewer
	})
	if p := seatProblem(r.status(), legacy); p != "" {
		t.Fatalf("the restored seat still has a problem: %s", p)
	}
	seq := r.post(reviewer, "back again", false)
	b := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(b, "back again") {
		t.Fatalf("the restored seat wasn't delivered to:\n%s", b)
	}
	r.wait("s1", "b1", false)
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
}

// A refusal stays final: a seat whose token is refused while the daemon retries isn't
// restored, and the daemon stops asking.
func TestRefusalWhileRetryingAJournalSeatIsFinal(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	legacy := reviewer
	legacy.MemberID = ""
	if err := r.journal.Bind(context.Background(), delivery.Binding{Agent: legacy, Session: delivery.SessionKey{Harness: "claude-code", ID: "s1"}, BoundAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	r.stop()
	var calls atomic.Int32
	r.resolve = func(context.Context, delivery.AgentRef) (delivery.AgentRef, error) {
		if calls.Add(1) == 1 {
			return delivery.AgentRef{}, errors.New("server_unreachable: connection refused")
		}
		return delivery.AgentRef{}, delivery.ErrUnauthorized
	}
	r.start()
	r.eventually("the refusal", time.Second, func() bool {
		return seatProblem(r.status(), legacy) == "unauthorized"
	})
	n := calls.Load()
	r.clock.Advance(5 * time.Minute)
	if got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"}); len(got.Agents) != 0 {
		t.Fatalf("a refused seat was restored: %+v", got)
	}
	if got := calls.Load(); got != n {
		t.Fatalf("the daemon kept asking about a refused seat: %d calls, then %d", n, got)
	}
}

func TestVerifiedLegacyIdentityIsResolvedBeforeRestore(t *testing.T) {
	r := newRig(t)
	r.register("old-session", "old-boot")
	legacy := reviewer
	legacy.MemberID = ""
	if err := r.journal.Bind(context.Background(), delivery.Binding{Agent: legacy, Session: delivery.SessionKey{Harness: "claude-code", ID: "old-session"}, BoundAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.SetMode(context.Background(), legacy, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	r.stop()
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		ref.MemberID = "mem_original"
		return ref, nil
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "old-session"})
	if len(got.Agents) != 1 || got.Agents[0].MemberID != "mem_original" {
		t.Fatalf("restored identity: %+v", got)
	}
	mode := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &got.Agents[0]})
	if mode.Mode != delivery.ModeHumans {
		t.Fatalf("restored mode: %+v", mode)
	}
}

func TestUnresolvedLegacyStateIsNotGivenToASameNameReplacement(t *testing.T) {
	r := newRig(t)
	r.register("old-session", "old-boot")
	legacy := reviewer
	legacy.MemberID = ""
	if err := r.journal.Bind(context.Background(), delivery.Binding{Agent: legacy, Session: delivery.SessionKey{Harness: "claude-code", ID: "old-session"}, BoundAt: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.SetMode(context.Background(), legacy, delivery.ModeHumans); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.AddDelivery(context.Background(), delivery.Delivery{
		Agent:   legacy,
		Session: delivery.SessionKey{Harness: "claude-code", ID: "old-session"}, State: delivery.StateConfirmed,
		Seqs: []int{7}, CreatedAt: r.clock.Now(), UpdatedAt: r.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	r.stop()
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		if ref.MemberID == "" {
			return delivery.AgentRef{}, delivery.ErrUnauthorized
		}
		return ref, nil
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "old-session"})
	if len(got.Agents) != 0 {
		t.Fatalf("unverified seat was restored: %+v", got)
	}
	stale := r.call(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "old-session", Agent: &legacy})
	if stale.Error == nil || stale.Error.Code != "unauthorized" {
		t.Fatalf("unverified legacy name was rebound: %+v", stale)
	}
	fresh := reviewer
	fresh.MemberID = "mem_replacement"
	r.bind("claude-code", "old-session", fresh)
	mode := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &fresh})
	if mode.Mode != delivery.ModeFocused {
		t.Fatalf("replacement inherited old mode: %+v", mode)
	}
	deliveries, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
	if err != nil || len(deliveries) != 1 || deliveries[0].Agent.MemberID != "" {
		t.Fatalf("quarantined delivery changed identity: %+v, %v", deliveries, err)
	}
}

func TestBindWriteFailureLeavesThePreviousSeatBound(t *testing.T) {
	r := newRig(t)
	r.register("session", "boot")
	r.bind("claude-code", "session", reviewer)
	if err := r.journal.Close(); err != nil {
		t.Fatal(err)
	}
	resp := r.call(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "session", Agent: &planner})
	if resp.Error == nil || resp.Error.Code != "internal" {
		t.Fatalf("binding to a closed journal: %+v", resp)
	}
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "session"})
	if len(got.Agents) != 1 || got.Agents[0] != reviewer {
		t.Fatalf("failed write changed the active seat: %+v", got)
	}
}

func TestTemporaryOutageKeepsAnAlreadyVerifiedJournalSeat(t *testing.T) {
	r := newRig(t)
	ref := reviewer
	ref.MemberID = "mem_original"
	r.register("session", "boot")
	r.bind("claude-code", "session", ref)
	r.stop()
	r.resolve = func(context.Context, delivery.AgentRef) (delivery.AgentRef, error) {
		return delivery.AgentRef{}, errors.New("server unavailable")
	}
	r.start()
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "session"})
	if len(got.Agents) != 1 || got.Agents[0] != ref {
		t.Fatalf("outage dropped verified binding: %+v", got)
	}
}

func TestBindRejectsAMemberIDThatDoesNotMatchItsToken(t *testing.T) {
	r := newRig(t)
	r.register("session", "boot")
	r.stop()
	r.resolve = func(_ context.Context, ref delivery.AgentRef) (delivery.AgentRef, error) {
		ref.MemberID = "mem_actual"
		return ref, nil
	}
	r.start()
	r.register("session", "boot")
	ref := reviewer
	ref.MemberID = "mem_claimed"
	got := r.call(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "session", Agent: &ref})
	if got.Error == nil || got.Error.Code != "invalid_request" {
		t.Fatalf("mismatched seat identity: %+v", got)
	}
	seats := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "session"})
	if len(seats.Agents) != 0 {
		t.Fatalf("mismatch bound a seat: %+v", seats)
	}
}
