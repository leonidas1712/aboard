//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// The harness conformance kit's checks of focused delivery (spec/delivery.md, "Delivery
// modes"): a message that doesn't concern the agent never starts a turn and arrives at
// the start of its next one, through the harness's turn-start mechanism; messages close
// together wake the session once; a big backlog comes as a digest; and all, or its
// earlier name auto, wakes the session for every message. They run for every harness,
// each through its own way of delivering.

// asleepFor is how long a check waits to see that nothing wakes a session: longer than
// the daemon gathers messages before it hands a bundle over.
const asleepFor = 3 * time.Second

// writerBroadcasts posts body from writer to everyone: another agent's message that
// asks nothing and answers nothing of the reviewer's, so it doesn't concern it.
func writerBroadcasts(t *testing.T, e *env, body string) int {
	t.Helper()
	return int(field(t, e.run("say", "--as", "writer", body, "--json").json(t), "message.seq").(float64))
}

// neverWithin fails the test if cond holds at any point within d.
func neverWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatalf("%s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// kitTurnStarts requires the profile's turn-start capability.
func kitTurnStarts(t *testing.T, p support.Profile) {
	t.Helper()
	kitDelivers(t, p)
	if !p.Has("turn-start") {
		t.Skip("no turn-start capability: quiet messages wait for the agent's aboard inbox")
	}
}

// turnStart starts a turn as the owner's prompt does and returns what the harness's
// turn-start mechanism added to it: the prompt hook's additionalContext, or what an
// extension's turn_start was given (after which it reports the turn, as omp's
// agent_start does).
func (s *kitSession) turnStart(t *testing.T) string {
	t.Helper()
	if s.ext != nil {
		text := s.ext.turnStart()
		s.ext.send(map[string]any{"op": "prompt"})
		return text
	}
	h, _ := s.p.Hook("prompt")
	r := s.op("prompt", `"prompt":"the owner's next request"`)
	if r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	got := parseToolContext(t, r.stdout)
	if got.text != "" && got.event != h.Event {
		t.Fatalf("the prompt hook answered for %q, want the event it ran for, %s", got.event, h.Event)
	}
	return got.text
}

// asleep checks the idle session is given nothing for asleepFor: its waiting hook w
// still waits, its extension is sent nothing, or its harness's queue takes nothing.
func (s *kitSession) asleep(t *testing.T, w *proc, why string) {
	t.Helper()
	switch {
	case s.ext != nil:
		select {
		case f := <-s.ext.frames:
			t.Fatalf("%s: the extension was sent %+v", why, f)
		case <-time.After(asleepFor):
		}
	case s.p.WaitsForIdle():
		if !w.running(asleepFor) {
			t.Fatalf("%s: the waiting hook woke the session\n%s", why, w.wait(time.Second))
		}
	default:
		queued := len(kitFakes[s.p.Harness].queued(s.e, s.id))
		neverWithin(t, asleepFor, why+": the harness's queue took a bundle", func() bool {
			return len(kitFakes[s.p.Harness].queued(s.e, s.id)) > queued
		})
	}
}

// turnStart asks, on a connection of its own, what a starting turn is given, as omp's
// extension does in before_agent_start.
func (c *extClient) turnStart() string {
	c.t.Helper()
	conn, err := net.Dial("unix", c.e.socketPath())
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	req := map[string]any{
		"v": 1, "op": "turn_start", "harness": c.hello["harness"], "session": c.hello["session"], "boot": c.hello["boot"],
		"started": time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, _ := json.Marshal(req)
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		c.t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		c.t.Fatalf("read the turn start's answer: %v", err)
	}
	var f extFrame
	if err := json.Unmarshal([]byte(line), &f); err != nil || f.Error != nil {
		c.t.Fatalf("turn_start: %s", line)
	}
	return strings.TrimSpace(f.Bundle)
}

// kitQuietWaitsForTheNextTurn checks that another agent's message to everyone doesn't
// wake an idle agent, arrives in the "while you were away" block when its owner's next
// prompt starts a turn, and counts as received once the session's next event confirms it.
func kitQuietWaitsForTheNextTurn(t *testing.T, p support.Profile) {
	kitTurnStarts(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	if got := field(t, s.run("status", "--json").json(t), "delivery"); got != "focused" {
		t.Fatalf("an agent nobody set is %v, want focused", got)
	}
	w := s.idle()
	seq := writerBroadcasts(t, e, "FYI: the build is green.")
	s.asleep(t, w, "a message to everyone that doesn't concern the agent")

	text := s.turnStart(t)
	for _, want := range []string{
		"Aboard: while you were away, 1 other message arrived on writer-reviewer", `quiet="true"`,
		`seq="` + strconv.Itoa(seq) + `"`, "FYI: the build is green.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the turn's start lacks %q:\n%s", want, text)
		}
	}
	if w != nil {
		if r := w.wait(5 * time.Second); r.code != 0 || strings.Contains(r.stderr, "aboard-message") {
			t.Fatalf("the prompt should release the waiting hook without a bundle\n%s", r)
		}
	}
	if again := s.turnStart(t); again != "" {
		t.Fatalf("the next turn's start was given the message again:\n%s", again)
	}
	s.idle() // the turn ends: the session's next event confirms what its start was given
	eventually(t, 10*time.Second, "the quiet message to be acknowledged", func() bool { return s.unread() == 0 })
}

// kitQuietWhileBusy checks a message that doesn't concern a busy agent is named by the
// waiting notice, starts no turn when the busy one ends, and arrives with the next one.
func kitQuietWhileBusy(t *testing.T, p support.Profile) {
	kitTurnStarts(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	if text := s.turnStart(t); text != "" {
		t.Fatalf("a turn with nothing waiting was given:\n%s", text)
	}
	seq := writerBroadcasts(t, e, "FYI: deploy at five.")
	if p.Delivery.WaitingNotice && (p.Has("tool-boundary") || p.Has("extension")) {
		var notice string
		eventually(t, 10*time.Second, "a tool boundary to name the waiting message", func() bool {
			notice = s.toolContext(t)
			return notice != ""
		})
		if !strings.Contains(notice, "<aboard-notice") || !strings.Contains(notice, "#"+strconv.Itoa(seq)) || strings.Contains(notice, "deploy at five") {
			t.Fatalf("want a notice naming #%d without its content:\n%s", seq, notice)
		}
	}
	w := s.idle()
	s.asleep(t, w, "the busy turn ended with only a quiet message waiting")
	if text := s.turnStart(t); !strings.Contains(text, "FYI: deploy at five.") || !strings.Contains(text, `quiet="true"`) {
		t.Fatalf("the next turn's start lacks the quiet message:\n%s", text)
	}
	s.idle()
	eventually(t, 10*time.Second, "the quiet message to be acknowledged", func() bool { return s.unread() == 0 })
}

// kitWakingCarriesTheQuiet checks a message addressed to the agent wakes it, and its
// bundle carries the quiet message that waited, set apart after it.
func kitWakingCarriesTheQuiet(t *testing.T, p support.Profile) {
	kitTurnStarts(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	writerBroadcasts(t, e, "FYI: lunch at one.")
	s.asleep(t, w, "a message to everyone that doesn't concern the agent")
	direct := writerSays(t, e, "Reviewer: please look at notes.md.")
	bundle := s.nextBundle(w, 10*time.Second)
	at, quiet := strings.Index(bundle, `seq="`+strconv.Itoa(direct)+`"`), strings.Index(bundle, "while you were away")
	if at < 0 || quiet < 0 || at > quiet || !strings.Contains(bundle, "FYI: lunch at one.") {
		t.Fatalf("the waking bundle should hold the message to the agent, then the quiet one apart:\n%s", bundle)
	}
}

// kitCombinesWakes checks messages that arrive within about two seconds of each other
// wake an idle session once.
func kitCombinesWakes(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	first := writerSays(t, e, "first of two")
	second := writerSays(t, e, "second of two")
	bundle := s.nextBundle(w, 10*time.Second)
	for _, seq := range []int{first, second} {
		if !strings.Contains(bundle, `seq="`+strconv.Itoa(seq)+`"`) {
			t.Fatalf("two messages sent together should wake the session once; the bundle lacks #%d:\n%s", seq, bundle)
		}
	}
}

// kitDigest checks a backlog over the digest's threshold comes as one bundle: the
// message that concerns the agent in full, one line for each other, and the commands
// to read them; every message in it counts as received.
func kitDigest(t *testing.T, p support.Profile) {
	kitTurnStarts(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	for i := 1; i <= 11; i++ {
		writerBroadcasts(t, e, "status update "+strconv.Itoa(i)+"\nwith a second line no digest shows")
	}
	direct := writerSays(t, e, "Reviewer: sign off on the release?")
	bundle := s.nextBundle(w, 10*time.Second)
	for _, want := range []string{
		"12 messages arrived on writer-reviewer", `<aboard-digest board="writer-reviewer" count="11">`,
		"@writer → all: status update 1\n", `seq="` + strconv.Itoa(direct) + `"`, "Reviewer: sign off on the release?",
		"aboard read --around", "aboard read --threads",
	} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("the digest lacks %q:\n%s", want, bundle)
		}
	}
	if strings.Contains(bundle, "second line no digest shows") {
		t.Fatalf("a summarized message was given in full:\n%s", bundle)
	}
	if p.WaitsForIdle() {
		s.idle() // the woken turn ends: the session's next event confirms the bundle
	}
	eventually(t, 10*time.Second, "every message in the digest to be acknowledged", func() bool { return s.unread() == 0 })
}

// kitAllMode checks all, set by its earlier name auto, wakes the session for every
// message, as delivery did before focused.
func kitAllMode(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	expectLines(t, e.run("delivery", "auto", "--as", "reviewer"),
		"reviewer on writer-reviewer: delivery now all (wakes for every message)")
	w := s.idle()
	seq := writerBroadcasts(t, e, "FYI: everyone, the build is green.")
	bundle := s.nextBundle(w, 10*time.Second)
	if !strings.Contains(bundle, `seq="`+strconv.Itoa(seq)+`"`) || strings.Contains(bundle, "while you were away") {
		t.Fatalf("in all mode a message to everyone should wake the session, in full:\n%s", bundle)
	}
}

// kitModeChanged checks a changed delivery mode reaches the session at its next turn's
// start, through the harness's turn-start mechanism, as one line naming the new mode and
// its rule, once, and in every mode, humans included, where the start is given no
// messages.
func kitModeChanged(t *testing.T, p support.Profile) {
	kitTurnStarts(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	e.run("delivery", "humans", "--as", "reviewer")
	s.asleep(t, w, "a changed delivery mode")

	want := "Aboard: your delivery mode on writer-reviewer changed from focused to humans. " + humansRule
	if text := s.turnStart(t); text != want {
		t.Fatalf("the turn's start should be the changed mode alone\nwant: %s\ngot:  %s", want, text)
	}
	if again := s.turnStart(t); again != "" {
		t.Fatalf("the next turn's start told the changed mode again:\n%s", again)
	}
}
