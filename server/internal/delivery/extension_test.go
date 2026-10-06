package delivery_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// ext is a harness extension's connection to the daemon, as omp's extension holds one.
type ext struct {
	t      *testing.T
	clock  *clock.Fake
	conn   net.Conn
	frames chan delivery.Response
}

// connect opens an extension connection with hello and returns it with the daemon's
// first answer: welcome, or an error.
func (r *rig) connect(hello delivery.Request) (*ext, delivery.Response) {
	r.t.Helper()
	c := r.dial()
	r.t.Cleanup(func() { _ = c.Close() })
	e := &ext{t: r.t, clock: r.clock, conn: c, frames: make(chan delivery.Response, 16)}
	go func() {
		defer close(e.frames)
		br := bufio.NewReader(c)
		for {
			var resp delivery.Response
			if err := delivery.ReadFrame(br, &resp); err != nil {
				return
			}
			e.frames <- resp
		}
	}()
	hello.V, hello.Op = delivery.ProtocolVersion, delivery.OpHello
	go func() { _ = delivery.WriteFrame(c, hello) }()
	first := e.next()
	return e, first
}

// hello connects omp's extension for session o1 and fails unless it is welcomed.
func (r *rig) hello(boot string, resumed bool) *ext {
	r.t.Helper()
	e, welcome := r.connect(delivery.Request{
		Harness: "omp", Session: "o1", Boot: boot, Source: "startup", Resumed: resumed,
		Process: &delivery.Process{PID: 51234},
	})
	if welcome.Event != delivery.EventWelcome {
		r.t.Fatalf("want welcome, got %+v", welcome)
	}
	return e
}

func (e *ext) send(req delivery.Request) {
	e.t.Helper()
	req.V = delivery.ProtocolVersion
	if err := delivery.WriteFrame(e.conn, req); err != nil {
		e.t.Fatalf("send %s: %v", req.Op, err)
	}
}

// next returns the next frame the daemon sends on the connection.
func (e *ext) next() delivery.Response {
	e.t.Helper()
	resp, ok := await(e.t, e.clock, e.frames, "the extension")
	if !ok {
		e.t.Fatal("the daemon closed the extension's connection")
	}
	return resp
}

// deliver waits for a bundle, and confirms it unless confirm is false.
func (e *ext) deliver(confirm bool) delivery.Response {
	e.t.Helper()
	resp := e.next()
	if resp.Event != delivery.EventDeliver || resp.ID == 0 || resp.Bundle == "" {
		e.t.Fatalf("want a delivery with an id, got %+v", resp)
	}
	if confirm {
		e.send(delivery.Request{Op: delivery.OpReceived, ID: resp.ID})
	}
	return resp
}

// closed waits until the daemon has closed the connection.
func (e *ext) closed() {
	e.t.Helper()
	select {
	case resp, ok := <-e.frames:
		if ok {
			e.t.Fatalf("want the connection closed, got %+v", resp)
		}
	case <-time.After(within):
		e.t.Fatal("the daemon kept the connection open")
	}
}

func TestExtensionIdleSessionGetsABundleAndItsReceivedConfirmsIt(t *testing.T) {
	r := newRig(t)
	e := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	one := r.post(reviewer, "one", false)

	d := e.deliver(false)
	if !strings.Contains(d.Bundle, "one") {
		t.Fatalf("bundle:\n%s", d.Bundle)
	}
	if got := r.server.Cursor(reviewer); got != 0 {
		t.Fatalf("acknowledged up to %d before the extension confirmed", got)
	}
	e.send(delivery.Request{Op: delivery.OpReceived, ID: d.ID})
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == one })
	r.presence(reviewer, delivery.PresenceWorking)
	// The woken turn ends: idle again, the next message goes over the same connection.
	e.send(delivery.Request{Op: delivery.OpPrompt})
	e.send(delivery.Request{Op: delivery.OpTurnEnd})
	r.presence(reviewer, delivery.PresenceIdle)
	two := r.post(reviewer, "two", false)
	if b := e.deliver(true).Bundle; !strings.Contains(b, "two") || strings.Contains(b, "one") {
		t.Fatalf("second bundle:\n%s", b)
	}
	r.eventually("the second acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == two })
}

func TestExtensionBusySessionGetsPeersAtTheTurnsEndAndTheOwnerAtABoundary(t *testing.T) {
	r := newRig(t)
	e := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	e.send(delivery.Request{Op: delivery.OpPrompt})
	r.presence(reviewer, delivery.PresenceWorking)
	r.post(reviewer, "peer one", false)
	r.post(reviewer, "peer two", true)
	r.postFromOwner(reviewer, "owner: stop")

	var mid string
	r.eventually("the owner's message at a tool boundary", 0, func() bool {
		mid = r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "omp", Session: "o1", Boot: "b1"}).Bundle
		return mid != ""
	})
	if !strings.Contains(mid, "owner: stop") || strings.Contains(mid, "peer") {
		t.Fatalf("a tool boundary added:\n%s", mid)
	}
	e.send(delivery.Request{Op: delivery.OpTurnEnd})
	b := e.deliver(true).Bundle
	if !strings.Contains(b, `count="2"`) || !strings.Contains(b, "peer one") || strings.Contains(b, "owner: stop") {
		t.Fatalf("want both peers' messages in one bundle at the turn's end:\n%s", b)
	}
}

// A connection that closes without a goodbye closes the session; a bundle sent on it and
// never confirmed goes again, with the same id, when the extension reconnects.
func TestExtensionReconnectGetsTheUnconfirmedBundleAgain(t *testing.T) {
	r := newRig(t)
	e := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	seq := r.post(reviewer, "are you there", false)
	first := e.deliver(false)
	_ = e.conn.Close()
	r.eventually("the session to close", 0, func() bool { return r.status().OpenSessions == 0 })
	r.presence(reviewer, delivery.PresenceNoSession)

	again := r.hello("b1", true).deliver(true)
	if again.ID != first.ID || again.Bundle != first.Bundle {
		t.Fatalf("after reconnecting, got delivery %d:\n%s\nwant %d again:\n%s", again.ID, again.Bundle, first.ID, first.Bundle)
	}
	r.eventually("the acknowledgement", 0, func() bool { return r.server.Cursor(reviewer) == seq })
}

// A hello for a session that has a connection replaces it: the older one is told release
// and closed, and what it reports afterwards changes nothing.
func TestExtensionHelloReplacesTheSessionsOlderConnection(t *testing.T) {
	r := newRig(t)
	old := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	newer := r.hello("b2", false)
	if resp := old.next(); resp.Event != delivery.EventRelease {
		t.Fatalf("the older connection got %+v, want release", resp)
	}
	old.closed()
	r.post(reviewer, "for the newer one", false)
	if b := newer.deliver(true).Bundle; !strings.Contains(b, "for the newer one") {
		t.Fatalf("bundle:\n%s", b)
	}
	if st := r.status(); st.OpenSessions != 1 {
		t.Fatalf("%d open sessions, want the one", st.OpenSessions)
	}
}

func TestExtensionGoodbyeClosesTheSession(t *testing.T) {
	r := newRig(t)
	e := r.hello("b1", false)
	r.bind("omp", "o1", reviewer)
	r.presence(reviewer, delivery.PresenceIdle)
	e.send(delivery.Request{Op: delivery.OpGoodbye})
	e.closed()
	r.presence(reviewer, delivery.PresenceNoSession)
	if st := r.status(); st.OpenSessions != 0 {
		t.Fatalf("%d open sessions after goodbye", st.OpenSessions)
	}
	// The same session coming back is bound again to its agent.
	_, welcome := r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b2", Source: "resume"})
	if !welcome.Reopened || len(welcome.Agents) != 1 || welcome.Agents[0] != reviewer {
		t.Fatalf("a resumed session's welcome: %+v", welcome)
	}
}

func TestExtensionHelloFromASubagentIsRefused(t *testing.T) {
	r := newRig(t)
	_, resp := r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b1", Subagent: "0-Explore"})
	if resp.Error == nil || resp.Error.Code != "subagent_session" {
		t.Fatalf("a subagent's hello: %+v", resp)
	}
}

// An extension names only its process id; the daemon reads the start time, so a session
// whose omp is gone closes even when the daemon restarted and its connection is long gone.
func TestExtensionSessionClosesWhenItsProcessIsGone(t *testing.T) {
	r := newRig(t)
	r.hello("b1", false)
	r.stop()
	r.procs.Kill(delivery.Process{PID: 51234, Start: 1})
	r.start()
	r.eventually("the session of the gone process to close", delivery.LivenessCheck, func() bool { return r.status().OpenSessions == 0 })
}

// The extension connection's examples in spec/control.md play against the daemon: each
// answer has the fields and the event the page shows.
func TestControlSpecExtensionExamplesPlayAgainstTheDaemon(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/control.md")
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := bytes.Cut(raw, []byte("## The extension connection"))
	if !found {
		t.Fatal("spec/control.md has no section on the extension connection")
	}
	section := string(after)
	var lines []string
	for _, m := range regexp.MustCompile("(?s)```json\n(.*?)```").FindAllStringSubmatch(section, -1) {
		lines = append(lines, strings.Split(strings.TrimSpace(m[1]), "\n")...)
	}
	inline := map[string]string{}
	for _, m := range regexp.MustCompile("`(\\{\"v\":[^`]*\\})`").FindAllStringSubmatch(section, -1) {
		var f struct {
			Op    string `json:"op"`
			Event string `json:"event"`
		}
		if json.Unmarshal([]byte(m[1]), &f) == nil {
			inline[f.Op+f.Event] = m[1]
		}
	}
	if len(lines) != 2 || len(inline) < 6 {
		t.Fatalf("found %d hello/welcome lines and %d inline messages in the extension section of spec/control.md", len(lines), len(inline))
	}
	var hello delivery.Request
	var wantWelcome map[string]any
	if json.Unmarshal([]byte(lines[0]), &hello) != nil || json.Unmarshal([]byte(lines[1]), &wantWelcome) != nil {
		t.Fatalf("the spec's hello and welcome aren't JSON:\n%s", strings.Join(lines, "\n"))
	}
	r := newRig(t)
	// The spec's welcome names an agent, so the session fills one before the connection
	// the test plays on. That second connection releases the first, as the page shows.
	first, _ := r.connect(hello)
	r.bind(hello.Harness, hello.Session, reviewer)
	e, welcome := r.connect(hello)
	sameShape(t, "welcome", wantWelcome, welcome)
	var wantRelease map[string]any
	_ = json.Unmarshal([]byte(inline["release"]), &wantRelease)
	sameShape(t, "release", wantRelease, first.next())

	play := func(key string) {
		t.Helper()
		var req delivery.Request
		if err := json.Unmarshal([]byte(inline[key]), &req); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		e.send(req)
	}
	play("prompt")
	r.presence(reviewer, delivery.PresenceWorking)
	play("turn_end")
	r.presence(reviewer, delivery.PresenceIdle)
	r.post(reviewer, "hello from the spec", false)
	got := e.next()
	var wantDeliver map[string]any
	_ = json.Unmarshal([]byte(inline["deliver"]), &wantDeliver)
	sameShape(t, "deliver", wantDeliver, got)
	e.send(delivery.Request{Op: delivery.OpReceived, ID: got.ID})
	play("goodbye")
	e.closed()
	r.presence(reviewer, delivery.PresenceNoSession)
}

// sameShape checks a daemon's answer has exactly the fields a spec example has, and the
// same event.
func sameShape(t *testing.T, what string, want map[string]any, got delivery.Response) {
	t.Helper()
	raw, _ := json.Marshal(got)
	var have map[string]any
	_ = json.Unmarshal(raw, &have)
	keys := func(m map[string]any) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(keys(want), keys(have)) || want["event"] != have["event"] {
		t.Errorf("%s: the daemon answered %s; the spec shows fields %v and event %v", what, raw, keys(want), want["event"])
	}
}

func TestExtensionNegotiatesOnlyHandoffCapability(t *testing.T) {
	r := newRig(t)
	var hello delivery.Request
	if err := json.Unmarshal([]byte(`{"harness":"omp","session":"o1","boot":"b1","capabilities":["unknown","handoff-v1"]}`), &hello); err != nil {
		t.Fatal(err)
	}
	_, welcome := r.connect(hello)
	raw, err := json.Marshal(welcome)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["capabilities"].([]any); len(got) != 1 || got[0] != "handoff-v1" {
		t.Fatalf("negotiated capabilities: %s", raw)
	}
}
