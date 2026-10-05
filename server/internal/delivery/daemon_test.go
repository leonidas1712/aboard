package delivery_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/delivery/extension"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

const serverURL = "http://127.0.0.1:7400"

var (
	reviewer = delivery.AgentRef{Server: serverURL, Board: "docs", Name: "reviewer"}
	planner  = delivery.AgentRef{Server: serverURL, Board: "plans", Name: "planner"}
)

// within bounds every wait in these tests; nothing should come close to it.
const within = 10 * time.Second

// rig runs a daemon with fake harnesses, a fake server and a fake clock, and a journal
// on disk that survives restarts.
type rig struct {
	t       *testing.T
	clock   *clock.Fake
	claude  *deliverytest.FakeAdapter
	codex   *deliverytest.FakeAdapter
	server  *deliverytest.FakeServer
	procs   *deliverytest.FakeProcesses
	path    string
	ctl     *deliverytest.PipeControl
	journal *sqlitejournal.Journal
	tickets launchtickets.Dir
	seats   delivery.Seats
	cancel  context.CancelFunc
	done    chan error
	remote  delivery.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigWithServer(t, nil)
}

func newRigWithServer(t *testing.T, wrap func(delivery.Server) delivery.Server) *rig {
	t.Helper()
	r := &rig{
		t: t, clock: clock.NewFake(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)),
		claude:  deliverytest.NewFakeAdapter("claude-code", true),
		codex:   deliverytest.NewFakeAdapter("codex", false),
		server:  deliverytest.NewFakeServer(),
		procs:   deliverytest.NewFakeProcesses(),
		path:    filepath.Join(t.TempDir(), "delivery.db"),
		tickets: launchtickets.Dir(t.TempDir()),
	}
	if wrap != nil {
		r.remote = wrap(r.server)
	}
	r.start()
	t.Cleanup(r.stop)
	return r
}

func (r *rig) start() {
	r.t.Helper()
	j, err := sqlitejournal.Open(context.Background(), r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	r.journal, r.ctl = j, deliverytest.NewPipeControl()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan error, 1)
	cfg := delivery.Config{
		Journal: j, Adapters: []delivery.Adapter{r.claude, r.codex, extension.Adapter{Name: "omp"}},
		Connect: func(string) delivery.Server {
			if r.remote != nil {
				return r.remote
			}
			return r.server
		},
		Control: r.ctl, Processes: r.procs, Clock: r.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PID: 4182,
		Tickets: r.tickets, Seats: r.seats,
	}
	go func() { r.done <- delivery.Run(ctx, cfg) }()
}

// stop stops the daemon, as a crash would: nothing is flushed beyond what was written.
func (r *rig) stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			r.t.Errorf("daemon: %v", err)
		}
	case <-time.After(within):
		r.t.Error("the daemon didn't stop")
	}
	_ = r.journal.Close()
	r.cancel = nil
}

func (r *rig) restart() {
	r.stop()
	r.start()
}

func (r *rig) dial() net.Conn {
	r.t.Helper()
	c, err := r.ctl.Dial()
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

// call sends one request and returns the answer.
func (r *rig) call(req delivery.Request) delivery.Response {
	r.t.Helper()
	c := r.dial()
	defer func() { _ = c.Close() }()
	req.V = delivery.ProtocolVersion
	go func() { _ = delivery.WriteFrame(c, req) }()
	var resp delivery.Response
	if err := delivery.ReadFrame(bufio.NewReader(c), &resp); err != nil {
		r.t.Fatalf("%s: %v", req.Op, err)
	}
	return resp
}

func (r *rig) ok(req delivery.Request) delivery.Response {
	r.t.Helper()
	resp := r.call(req)
	if resp.Error != nil {
		r.t.Fatalf("%s: %+v", req.Op, resp.Error)
	}
	return resp
}

// register starts a Claude Code session, as its session-start hook does.
func (r *rig) register(id, boot string) {
	r.t.Helper()
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: id, Boot: boot})
}

func (r *rig) bind(harness, id string, agent delivery.AgentRef) {
	r.t.Helper()
	r.ok(delivery.Request{Op: delivery.OpBind, Harness: harness, Session: id, Agent: &agent})
}

func (r *rig) status() *delivery.Status {
	r.t.Helper()
	return r.ok(delivery.Request{Op: delivery.OpStatus}).Status
}

// hook is a waiting stop hook.
type hook struct {
	t      *testing.T
	clock  *clock.Fake
	conn   net.Conn
	events chan delivery.Response
}

// passStep and passLimit are how the fake clock moves while a test waits for the daemon
// to hand something over: by passStep every few milliseconds, up to passLimit in all, as
// time passes for a real daemon, so messages it gathers for QueueGather go.
const (
	passStep  = 250 * time.Millisecond
	passLimit = delivery.QueueGather + time.Second
)

// await returns the next value from ch, moving c on as time would pass meanwhile.
func await[T any](t *testing.T, c *clock.Fake, ch <-chan T, what string) (T, bool) {
	t.Helper()
	deadline := time.After(within)
	moved := time.Duration(0)
	for {
		select {
		case v, ok := <-ch:
			return v, ok
		case <-deadline:
			t.Fatalf("%s got nothing", what)
			var zero T
			return zero, false
		case <-time.After(5 * time.Millisecond): // a poll interval, not a wait for the daemon
			if c != nil && moved < passLimit {
				c.Advance(passStep)
				moved += passStep
			}
		}
	}
}

// wait connects a stop hook for a Claude Code session.
func (r *rig) wait(id, boot string, resumed bool) *hook {
	r.t.Helper()
	c := r.dial()
	h := &hook{t: r.t, clock: r.clock, conn: c, events: make(chan delivery.Response, 4)}
	registered := make(chan struct{})
	r.t.Cleanup(func() { _ = c.Close() })
	go func() {
		_ = delivery.WriteFrame(c, delivery.Request{
			V: delivery.ProtocolVersion, Op: delivery.OpWait, Harness: "claude-code",
			Session: id, Boot: boot, Resumed: resumed,
		})
	}()
	go func() {
		defer close(h.events)
		br := bufio.NewReader(c)
		for {
			var resp delivery.Response
			if err := delivery.ReadFrame(br, &resp); err != nil {
				return
			}
			if resp.Event == delivery.EventWaiting {
				close(registered)
				continue
			}
			if resp.Event == delivery.EventDeliver {
				_ = delivery.WriteFrame(c, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpReceived})
			}
			h.events <- resp
			if resp.Event != "" || resp.Error != nil {
				return
			}
		}
	}()
	select {
	case <-registered:
	case resp := <-h.events:
		r.t.Fatalf("wait refused: %+v", resp)
	case <-time.After(within):
		r.t.Fatal("the wait wasn't registered")
	}
	return h
}

// next returns what the hook got: a bundle or a release.
func (h *hook) next() delivery.Response {
	h.t.Helper()
	resp, ok := await(h.t, h.clock, h.events, "the hook")
	if !ok {
		h.t.Fatal("the hook's connection closed without an event")
	}
	return resp
}

func (h *hook) bundle() string {
	h.t.Helper()
	resp := h.next()
	if resp.Event != delivery.EventDeliver {
		h.t.Fatalf("want a bundle, got %+v", resp)
	}
	return resp.Bundle
}

// eventually polls cond until it holds, moving the fake clock forward by step each time.
func (r *rig) eventually(what string, step time.Duration, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s", what)
		}
		if step > 0 {
			r.clock.Advance(step)
		}
		<-time.After(2 * time.Millisecond) // a poll interval, not a wait for the daemon
	}
}

func (r *rig) post(to delivery.AgentRef, body string, urgent bool) int {
	return r.server.Post(to, delivery.Message{Body: body, Urgent: urgent})
}

// postFromOwner posts a message from the agent's owner, the one sender that reaches a
// busy session mid-turn.
func (r *rig) postFromOwner(to delivery.AgentRef, body string) int {
	return r.server.Post(to, delivery.Message{Body: body, FromHuman: true, FromName: "alex", Sender: "owner"})
}

func TestIdleSessionGetsOneBundleAndAcksOnlyAfterConfirmation(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.post(reviewer, "one", false)
	two := r.post(reviewer, "two", false)

	b := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(b, `count="2"`) || strings.Index(b, "one") > strings.Index(b, "two") {
		t.Fatalf("want one bundle of two, oldest first:\n%s", b)
	}
	if got := r.server.Cursor(reviewer); got != 0 {
		t.Fatalf("acknowledged up to %d before the session confirmed", got)
	}
	// The woken turn ends and the stop hook connects again: that confirms.
	r.wait("s1", "b1", false)
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == two })
}

func TestPromptReleasesTheWaitingHookWithoutADelivery(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	h := r.wait("s1", "b1", false)
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if resp := h.next(); resp.Event != delivery.EventRelease {
		t.Fatalf("want a release, got %+v", resp)
	}
}

func TestOwnersMessageGoesToTheToolHookAndOthersWaitForIdle(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "claude-code", Session: "s1", Boot: "b1"})
	ordinary := r.post(reviewer, "ordinary", true)
	urgent := r.postFromOwner(reviewer, "build broken")

	var got string
	r.eventually("the owner's message", 0, func() bool {
		got = r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "s1", Boot: "b1"}).Bundle
		return got != ""
	})
	if !strings.Contains(got, "build broken") || strings.Contains(got, "ordinary") {
		t.Fatalf("tool hook bundle:\n%s", got)
	}
	b := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(b, "ordinary") || strings.Contains(b, "build broken") {
		t.Fatalf("idle bundle:\n%s", b)
	}
	r.wait("s1", "b1", false)
	r.eventually("both acknowledged", 0, func() bool { return r.server.Cursor(reviewer) == urgent })
	if acks := r.server.Acks(); len(acks) == 0 || acks[0] < ordinary {
		t.Fatalf("acks %v moved past the ordinary message before it was confirmed", acks)
	}
}

func TestBundlesStayWithinTheLimit(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	big := strings.Repeat("x", 12<<10)
	r.post(reviewer, "a"+big, false)
	r.post(reviewer, "b"+big, false)
	r.post(reviewer, "c"+big, false)

	first := r.wait("s1", "b1", false).bundle()
	if len(first) > delivery.BundleLimit || !strings.Contains(first, `count="2"`) {
		t.Fatalf("first bundle: %d bytes, want two messages under %d", len(first), delivery.BundleLimit)
	}
	second := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(second, `<aboard-messages board="docs" count="1">`) || !strings.Contains(second, "c"+big[:10]) {
		t.Fatalf("second bundle:\n%.300s", second)
	}
}

func TestMessageTooLargeIsSkippedAndDoesNotBlockLaterOnes(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	huge := r.post(reviewer, strings.Repeat("y", 40<<10), false)
	small := r.post(reviewer, "small", false)

	b := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(b, "small") || strings.Contains(b, "yyyy") {
		t.Fatalf("bundle:\n%.200s", b)
	}
	r.wait("s1", "b1", false)
	r.eventually("the read position to move past both", 0, func() bool { return r.server.Cursor(reviewer) == small })
	st := r.status()
	if len(st.Skipped) != 1 || st.Skipped[0].Seqs[0] != huge || st.Skipped[0].Reason != delivery.ReasonTooLarge {
		t.Fatalf("skipped %+v", st.Skipped)
	}
}

func TestQueueingHarnessGathersMessagesAndConfirmsOnAcceptance(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.post(reviewer, "first", false)
	second := r.post(reviewer, "second", false)
	r.eventually("the queued bundle", 500*time.Millisecond, func() bool { return len(r.codex.Handed("t1")) > 0 })
	got := r.codex.Handed("t1")
	if len(got) != 1 || !strings.Contains(got[0], "first") || !strings.Contains(got[0], "second") {
		t.Fatalf("want both messages in one bundle, got %q", got)
	}
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == second })
}

func (r *rig) setMode(agent delivery.AgentRef, mode delivery.Mode) delivery.Response {
	r.t.Helper()
	return r.ok(delivery.Request{Op: delivery.OpMode, Agent: &agent, Mode: mode})
}

// A delivery mode is kept across restarts, and an agent nobody set is focused.
func TestDeliveryModeIsKeptAcrossRestarts(t *testing.T) {
	r := newRig(t)
	if got := r.setMode(reviewer, ""); got.Mode != delivery.ModeFocused || got.Changed {
		t.Fatalf("default mode: %+v", got)
	}
	if got := r.setMode(reviewer, delivery.ModeHumans); got.Mode != delivery.ModeHumans || !got.Changed {
		t.Fatalf("set humans: %+v", got)
	}
	r.restart()
	if got := r.setMode(reviewer, ""); got.Mode != delivery.ModeHumans {
		t.Fatalf("after a restart: %+v", got)
	}
	if got := r.setMode(planner, ""); got.Mode != delivery.ModeFocused {
		t.Fatalf("another agent: %+v", got)
	}
	if resp := r.call(delivery.Request{Op: delivery.OpMode, Agent: &reviewer, Mode: "sometimes"}); resp.Error == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// The default mode, set on the empty AgentRef, applies to every agent without its own.
func TestDefaultDeliveryModeAppliesToAgentsWithoutTheirOwn(t *testing.T) {
	r := newRig(t)
	r.setMode(reviewer, delivery.ModeHumans)
	if got := r.setMode(delivery.AgentRef{}, delivery.ModeOff); got.Mode != delivery.ModeOff || !got.Changed {
		t.Fatalf("set the default: %+v", got)
	}
	r.restart()
	if got := r.setMode(planner, ""); got.Mode != delivery.ModeOff {
		t.Fatalf("an agent without its own mode: %+v", got)
	}
	if got := r.setMode(reviewer, ""); got.Mode != delivery.ModeHumans {
		t.Fatalf("an agent with its own mode: %+v", got)
	}
}

// In humans mode a queueing harness gets a bundle only once a person writes, and that
// bundle carries the peer message that waited.
func TestHumansModeQueuesOnlyWhenAPersonWrites(t *testing.T) {
	r := newRig(t)
	r.setMode(reviewer, delivery.ModeHumans)
	r.bind("codex", "t1", reviewer)
	r.post(reviewer, "peer note", false)
	person := r.server.Post(reviewer, delivery.Message{Body: "from alex", FromName: "alex", FromHuman: true, Sender: "owner"})
	r.eventually("the queued bundle", 500*time.Millisecond, func() bool { return len(r.codex.Handed("t1")) > 0 })
	got := r.codex.Handed("t1")
	if len(got) != 1 || !strings.Contains(got[0], `count="2"`) || strings.Index(got[0], "peer note") > strings.Index(got[0], "from alex") {
		t.Fatalf("want one bundle with both messages, oldest first, got %q", got)
	}
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == person })
}

// While a Codex turn runs (between its prompt and stop hooks), the owner's messages are
// kept out of Codex's queue, where they would wait for the turn to end, and go to the
// next tool hook instead. Other messages, urgent ones too, still go to the queue.
func TestOwnersMessagesSkipTheQueueDuringACodexTurn(t *testing.T) {
	r := newRig(t)
	codexReq := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "t1"}
	}
	r.ok(codexReq(delivery.OpRegister))
	r.bind("codex", "t1", reviewer)
	r.ok(codexReq(delivery.OpPrompt))

	r.post(reviewer, "ordinary note", true)
	r.postFromOwner(reviewer, "stop: the build is broken")
	r.eventually("the ordinary message in the queue", 500*time.Millisecond, func() bool { return len(r.codex.Handed("t1")) > 0 })
	r.clock.Advance(time.Second)
	for _, b := range r.codex.Handed("t1") {
		if strings.Contains(b, "the build is broken") {
			t.Fatalf("the owner's message went into the queue during a turn:\n%s", b)
		}
	}
	if b := r.ok(codexReq(delivery.OpBoundary)).Bundle; !strings.Contains(b, "the build is broken") {
		t.Fatalf("the tool hook should get the owner's message, got %q", b)
	}
}

// The owner's message held for a tool call that never came goes into the queue when the
// turn ends.
func TestHeldOwnersMessageIsQueuedWhenTheCodexTurnEnds(t *testing.T) {
	r := newRig(t)
	codexReq := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "t1"}
	}
	r.ok(codexReq(delivery.OpRegister))
	r.bind("codex", "t1", reviewer)
	r.ok(codexReq(delivery.OpPrompt))
	r.postFromOwner(reviewer, "urgent but late")
	r.clock.Advance(5 * time.Second)
	if n := len(r.codex.Handed("t1")); n != 0 {
		t.Fatalf("queued during the turn: %q", r.codex.Handed("t1"))
	}
	r.ok(codexReq(delivery.OpTurnEnd))
	r.eventually("the owner's message in the queue", 500*time.Millisecond, func() bool {
		h := r.codex.Handed("t1")
		return len(h) == 1 && strings.Contains(h[0], "urgent but late")
	})
}

func TestBusyIsNeverAFailedAttempt(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.codex.SetBusy(true)
	r.post(reviewer, "hello", false)
	r.eventually("several busy answers", time.Second, func() bool { return r.codex.Attempts() >= 8 })
	if st := r.status(); len(st.Attention) != 0 {
		t.Fatalf("busy turned into attention: %+v", st.Attention)
	}
	r.codex.SetBusy(false)
	r.eventually("the delivery once not busy", time.Second, func() bool { return len(r.codex.Handed("t1")) == 1 })
}

func TestFailuresBackOffAndStopForAttentionAfterFiveAttempts(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.codex.FailWith(errors.New("codex queue: thread is locked"))
	r.post(reviewer, "hello", false)

	start := r.clock.Now()
	r.eventually("five attempts", 500*time.Millisecond, func() bool { return len(r.status().Attention) == 1 })
	if n := r.codex.Attempts(); n != delivery.MaxAttempts {
		t.Fatalf("%d attempts, want %d", n, delivery.MaxAttempts)
	}
	// 2 s gathering, then waits of 1, 2, 4 and 8 seconds.
	if took := r.clock.Now().Sub(start); took < 17*time.Second {
		t.Fatalf("five attempts took %s of clock time; the waits between them didn't back off", took)
	}
	r.clock.Advance(10 * time.Minute)
	if n := r.codex.Attempts(); n != delivery.MaxAttempts {
		t.Fatalf("attempts went on after attention: %d", n)
	}
	att := r.status().Attention[0]
	if att.Reason != delivery.ReasonHarnessError || att.Agent != reviewer {
		t.Fatalf("attention %+v", att)
	}
	if got := r.server.Cursor(reviewer); got != 0 {
		t.Fatalf("read position moved to %d without a delivery", got)
	}
}

func TestGoneCodexThreadNeedsAttentionWithItsReason(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.codex.MarkAbsent("t1")
	r.post(reviewer, "hello", false)
	r.eventually("attention", time.Second, func() bool { return len(r.status().Attention) == 1 })
	if reason := r.status().Attention[0].Reason; reason != delivery.ReasonTargetAbsent {
		t.Fatalf("reason %q", reason)
	}
}

func TestSubAgentSessionCannotBeBound(t *testing.T) {
	r := newRig(t)
	r.codex.MarkSubAgent("t-sub")
	resp := r.call(delivery.Request{Op: delivery.OpBind, Harness: "codex", Session: "t-sub", Agent: &reviewer})
	if resp.Error == nil || resp.Error.Code != delivery.ReasonSubAgent {
		t.Fatalf("bind of a sub-agent: %+v", resp)
	}
}

func TestCrashAfterHandingOverHandsTheBundleOverAgain(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	seq := r.post(reviewer, "please review", false)
	r.wait("s1", "b1", false).bundle()

	r.restart()
	b := r.wait("s1", "b1", true).bundle()
	if !strings.Contains(b, "please review") {
		t.Fatalf("after a crash the bundle wasn't handed over again:\n%s", b)
	}
	r.wait("s1", "b1", false)
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
}

// A daemon replaced while the turn a bundle woke still runs, as an upgrade does from that
// turn's own hooks, keeps the bundle handed: the turn's next event confirms it, and it
// isn't handed over a second time.
func TestRestartDuringAWokenTurnConfirmsWithoutHandingOverAgain(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	seq := r.post(reviewer, "please review", false)
	r.wait("s1", "b1", false).bundle()

	r.restart()
	h := r.wait("s1", "b1", false)
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
	select {
	case resp := <-h.events:
		t.Fatalf("the confirmed bundle was handed over again: %+v", resp)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCrashAfterConfirmingAcknowledgesWithoutHandingOverAgain(t *testing.T) {
	r := newRig(t)
	r.bind("codex", "t1", reviewer)
	r.server.FailAcks(true)
	seq := r.post(reviewer, "hello", false)
	// The harness has the bundle as soon as Hand is called, but it is confirmed only once
	// the journal says so; crash after that point, not before.
	r.eventually("the confirmed delivery", 500*time.Millisecond, func() bool {
		ds, err := r.journal.Deliveries(context.Background(), delivery.StateConfirmed)
		return err == nil && len(ds) == 1
	})

	r.restart()
	r.server.FailAcks(false)
	r.eventually("the acknowledgement after restart", 500*time.Millisecond, func() bool { return r.server.Cursor(reviewer) == seq })
	if n := len(r.codex.Handed("t1")); n != 1 {
		t.Fatalf("handed over %d times; a confirmed bundle must not be handed over again", n)
	}
}

func TestUnconfirmedBundleGoesToTheNextSessionForTheAgent(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.post(reviewer, "please review", false)
	r.wait("s1", "b1", false).bundle()
	r.ok(delivery.Request{Op: delivery.OpEnd, Harness: "claude-code", Session: "s1"})

	r.register("s2", "c1")
	r.bind("claude-code", "s2", reviewer)
	if b := r.wait("s2", "c1", false).bundle(); !strings.Contains(b, "please review") {
		t.Fatalf("next session got:\n%s", b)
	}
}

// A session fills one seat at a time. Binding it to another agent ends the old binding:
// nothing more is delivered for the old agent here, and the bundle handed for it but not
// confirmed goes to whichever session binds it next. The move survives a restart.
func TestBindingAnotherAgentMovesTheSession(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	if prev := r.ok(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "s1", Agent: &reviewer}).Previous; prev != nil {
		t.Fatalf("a session with no agent moved from %+v", prev)
	}
	r.post(reviewer, "handed before the move", false)
	r.wait("s1", "b1", false).bundle()

	resp := r.ok(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "s1", Agent: &planner})
	if resp.Previous == nil || *resp.Previous != reviewer {
		t.Fatalf("binding another agent should name the one it replaced, got %+v", resp.Previous)
	}
	again := r.ok(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "s1", Agent: &planner})
	if again.Previous != nil {
		t.Fatalf("binding the same agent again moved from %+v", again.Previous)
	}
	r.restart()
	r.post(reviewer, "for the old seat", false)
	r.post(planner, "for the new seat", false)
	b := r.wait("s1", "b1", false).bundle()
	if !strings.Contains(b, "for the new seat") || strings.Contains(b, "for the old seat") || strings.Contains(b, "handed before the move") {
		t.Fatalf("the moved session got:\n%s", b)
	}
	if got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"}).Agents; len(got) != 1 || got[0] != planner {
		t.Fatalf("agents bound to the moved session: %+v", got)
	}
	if got := r.server.Cursor(reviewer); got != 0 {
		t.Fatalf("the old agent's messages were acknowledged up to %d", got)
	}

	r.register("s2", "c1")
	r.bind("claude-code", "s2", reviewer)
	// The bundle handed before the move goes again as it was, then what came after it.
	if b = r.wait("s2", "c1", false).bundle(); !strings.Contains(b, "handed before the move") {
		t.Fatalf("the session that resumed the old agent got first:\n%s", b)
	}
	if b = r.wait("s2", "c1", false).bundle(); !strings.Contains(b, "for the old seat") {
		t.Fatalf("the session that resumed the old agent got next:\n%s", b)
	}
}

func TestNewBootHandsUnconfirmedBundlesOverAgain(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.post(reviewer, "please review", false)
	r.wait("s1", "b1", false).bundle()
	// The session's process was replaced (a resume) before its turn ended.
	r.register("s1", "b2")
	if b := r.wait("s1", "b2", false).bundle(); !strings.Contains(b, "please review") {
		t.Fatalf("after a new boot:\n%s", b)
	}
	if got := r.server.Cursor(reviewer); got != 0 {
		t.Fatalf("the new boot's event confirmed the old boot's bundle: cursor %d", got)
	}
}

func TestRevokedTokenStopsOnlyThatAgent(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.register("s2", "c1")
	r.bind("claude-code", "s1", reviewer)
	r.bind("claude-code", "s2", planner)
	r.server.Revoke(reviewer)
	r.post(reviewer, "for the revoked agent", false)
	r.post(planner, "for the planner", false)
	b := r.wait("s2", "c1", false).bundle()
	if !strings.Contains(b, "for the planner") || strings.Contains(b, "revoked") {
		t.Fatalf("bundle:\n%s", b)
	}
	st := r.status()
	if len(st.Agents) != 1 || st.Agents[0].Agent != reviewer || st.Agents[0].Reason != delivery.ReasonUnauthorized {
		t.Fatalf("agent problems %+v", st.Agents)
	}
}

// An agent whose board is gone stops for good: the daemon names the problem and makes
// no more requests for it, whatever happens on the board or in its session, while other
// agents carry on.
func TestAgentWhoseBoardIsGoneStopsForGood(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.register("s2", "c1")
	r.bind("claude-code", "s1", reviewer)
	r.bind("claude-code", "s2", planner)
	r.server.TakeOff(reviewer)
	r.post(reviewer, "after the removal", false)
	r.eventually("the reviewer's problem", 0, func() bool {
		st := r.status()
		return len(st.Agents) == 1 && st.Agents[0].Agent == reviewer && st.Agents[0].Reason == delivery.ReasonBoardGone
	})
	before := r.server.Requests(reviewer)

	r.post(reviewer, "more for the reviewer", false)
	r.wait("s1", "b1", false)
	// The second wait is answered only once the session has handled the first, which
	// asked for every inbox it holds.
	r.wait("s1", "b1", false)
	r.clock.Advance(delivery.PresenceRenew)
	// The planner's message goes through the same server connection after all of that.
	r.post(planner, "for the planner", false)
	if b := r.wait("s2", "c1", false).bundle(); !strings.Contains(b, "for the planner") {
		t.Fatalf("planner's bundle:\n%s", b)
	}
	if after := r.server.Requests(reviewer); after != before {
		t.Fatalf("%d more requests for the reviewer after its board was gone", after-before)
	}
}

// A presence report the board refuses stops the agent as an inbox read would, and
// nothing more is sent for it, even when the session moves on or closes.
func TestPresenceRefusedForAGoneBoardStopsTheAgent(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	r.server.TakeOff(reviewer)
	r.hookCall(delivery.OpPrompt)
	r.eventually("the presence refusal to stop the agent", 0, func() bool {
		st := r.status()
		return len(st.Agents) == 1 && st.Agents[0].Agent == reviewer && st.Agents[0].Reason == delivery.ReasonBoardGone
	})
	before := r.server.Requests(reviewer)
	r.post(reviewer, "after the refusal", false)
	r.wait("s1", "b1", false)
	r.hookCall(delivery.OpPrompt)
	r.clock.Advance(delivery.PresenceRenew)
	// Another session takes a different agent's place in s1, so reviewer leaves it.
	r.bind("claude-code", "s1", planner)
	r.post(planner, "for the planner", false)
	if b := r.wait("s1", "b1", false).bundle(); !strings.Contains(b, "for the planner") {
		t.Fatalf("planner's bundle:\n%s", b)
	}
	if after := r.server.Requests(reviewer); after != before {
		t.Fatalf("%d more requests for the reviewer after its board was gone", after-before)
	}
}

func TestAgentsListsWhatIsBoundToTheSession(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", planner)
	r.bind("claude-code", "s1", reviewer)
	got := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "s1"}).Agents
	if len(got) != 1 || got[0] != reviewer {
		t.Fatalf("agents %+v", got)
	}
	unknown := r.call(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "nope"})
	if unknown.Error == nil || unknown.Error.Code != "session_unknown" {
		t.Fatalf("unknown session: %+v", unknown)
	}
}

func TestDaemonStopsAfterTenMinutesWithoutAnOpenSession(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.clock.Advance(time.Hour)
	if st := r.status(); st.OpenSessions != 1 {
		t.Fatalf("open sessions %d", st.OpenSessions)
	}
	r.ok(delivery.Request{Op: delivery.OpEnd, Harness: "claude-code", Session: "s1"})
	r.eventually("the daemon to stop", time.Minute, r.stopped)
}

// stopped reports whether the daemon has stopped on its own.
func (r *rig) stopped() bool {
	select {
	case err := <-r.done:
		if err != nil {
			r.t.Fatalf("daemon: %v", err)
		}
		r.done <- nil
		return true
	default:
		return false
	}
}

// A session whose harness died without its end hook is closed: its messages are held,
// and with nothing else open the daemon stops.
func TestSessionWhoseHarnessDiedIsClosed(t *testing.T) {
	r := newRig(t)
	harness := delivery.Process{PID: 7101, Start: 1759320000}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s1", Boot: "b1", Process: &harness})
	r.bind("claude-code", "s1", reviewer)
	r.clock.Advance(time.Hour)
	if st := r.status(); st.OpenSessions != 1 {
		t.Fatalf("a live session should stay open: %d open", st.OpenSessions)
	}

	r.procs.Kill(harness)
	r.eventually("the dead session to close", delivery.LivenessCheck, func() bool { return r.status().OpenSessions == 0 })
	r.post(reviewer, "anyone there?", false)
	r.eventually("the daemon to stop", time.Minute, r.stopped)
}

// A session restored from the journal whose harness died while the daemon was down is
// closed as soon as the daemon starts.
func TestRestoredSessionWhoseHarnessDiedIsClosed(t *testing.T) {
	r := newRig(t)
	harness := delivery.Process{PID: 7102, Start: 1759320000}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s1", Boot: "b1", Process: &harness})
	r.stop()
	r.procs.Kill(harness)
	r.start()
	r.eventually("the dead session to close", 0, func() bool { return r.status().OpenSessions == 0 })
}

// Only a register sets the session's process; a later command run by the agent keeps
// it, so a command run from another process can't keep a dead session open.
func TestLaterRequestsDontReplaceTheSessionsProcess(t *testing.T) {
	r := newRig(t)
	harness := delivery.Process{PID: 7103, Start: 1759320000}
	other := delivery.Process{PID: 7104, Start: 1759320001}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s1", Boot: "b1", Process: &harness})
	r.ok(delivery.Request{Op: delivery.OpBind, Harness: "claude-code", Session: "s1", Agent: &reviewer, Process: &other})
	r.procs.Kill(harness)
	r.eventually("the dead session to close", delivery.LivenessCheck, func() bool { return r.status().OpenSessions == 0 })
}

// A stop hook that started before the latest prompt belongs to the turn before it. When
// its wait reaches the daemon after the prompt, it is released, not treated as idle.
func TestLateStopHookFromAnEarlierTurnIsReleased(t *testing.T) {
	r := newRig(t)
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	hookStarted := r.clock.Now()
	r.clock.Advance(time.Second)
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "claude-code", Session: "s1", Boot: "b1"})

	c := r.dial()
	defer func() { _ = c.Close() }()
	go func() {
		_ = delivery.WriteFrame(c, delivery.Request{
			V: delivery.ProtocolVersion, Op: delivery.OpWait, Harness: "claude-code",
			Session: "s1", Boot: "b1", Started: hookStarted,
		})
	}()
	br := bufio.NewReader(c)
	for {
		var resp delivery.Response
		if err := delivery.ReadFrame(br, &resp); err != nil {
			t.Fatalf("the late hook got no answer: %v", err)
		}
		if resp.Event == delivery.EventWaiting {
			continue
		}
		if resp.Event != delivery.EventRelease {
			t.Fatalf("want a release, got %+v", resp)
		}
		break
	}
	r.post(reviewer, "later", false)
	if b := r.wait("s1", "b1", false).bundle(); !strings.Contains(b, "later") {
		t.Fatalf("the next turn's stop hook should get the message:\n%s", b)
	}
}

func TestBadControlMessagesAreRejected(t *testing.T) {
	r := newRig(t)
	for name, req := range map[string]delivery.Request{
		"unknown operation": {V: delivery.ProtocolVersion, Op: "delete-everything"},
		"other version":     {V: 99, Op: delivery.OpStatus},
		"unknown harness":   {V: delivery.ProtocolVersion, Op: delivery.OpRegister, Harness: "nope", Session: "s"},
	} {
		c := r.dial()
		go func() { _ = delivery.WriteFrame(c, req) }()
		var resp delivery.Response
		if err := delivery.ReadFrame(bufio.NewReader(c), &resp); err != nil || resp.Error == nil {
			t.Errorf("%s: %+v %v", name, resp, err)
		}
		_ = c.Close()
	}
	c := r.dial()
	defer func() { _ = c.Close() }()
	go func() {
		_, _ = c.Write([]byte(`{"v":1,"op":"status","session":"` + strings.Repeat("x", delivery.MaxFrame) + "\"}\n"))
	}()
	var resp delivery.Response
	if err := delivery.ReadFrame(bufio.NewReader(c), &resp); err != nil || resp.Error == nil {
		t.Fatalf("oversized frame: %+v %v", resp, err)
	}
}
