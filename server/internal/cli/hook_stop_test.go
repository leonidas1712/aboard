package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type stopFrames struct {
	*bytes.Reader
	writes bytes.Buffer
}

func (f *stopFrames) Write(p []byte) (int, error) { return f.writes.Write(p) }

func TestCodexStopReturnsJSONContinuation(t *testing.T) {
	var frames bytes.Buffer
	bundle := "Aboard: new messages\nquoted sender text"
	if err := delivery.WriteFrame(&frames, delivery.Response{V: delivery.ProtocolVersion, Event: delivery.EventDeliver, Bundle: bundle, HandoffID: "h1"}); err != nil {
		t.Fatal(err)
	}
	conn := &stopFrames{Reader: bytes.NewReader(frames.Bytes())}
	var out, errout bytes.Buffer
	h := hookCall{harness: "codex", a: &app{env: Env{Stdout: &out, Stderr: &errout}}}
	code, done := h.waitOn(conn, delivery.Request{Op: delivery.OpWait})
	var got struct{ Decision, Reason string }
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("Stop output isn't JSON: %q (%v)", out.String(), err)
	}
	if code != 0 || !done || got.Decision != "block" || got.Reason != bundle || errout.Len() != 0 {
		t.Fatalf("wrong continuation: code=%d done=%v output=%+v stderr=%q", code, done, got, errout.String())
	}
	reader := bufio.NewReader(&conn.writes)
	var request delivery.Request
	if err := delivery.ReadFrame(reader, &request); err != nil {
		t.Fatal(err)
	}
	if err := delivery.ReadFrame(reader, &request); err != nil || request.Op != delivery.OpReceived || request.HandoffID != "h1" {
		t.Fatalf("hook didn't accept the named handoff: %+v (%v)", request, err)
	}
}

type failedHookOutput struct{}

func (failedHookOutput) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func TestCodexStopFailedOutputDoesNotAcceptHandoff(t *testing.T) {
	var frames bytes.Buffer
	if err := delivery.WriteFrame(&frames, delivery.Response{V: delivery.ProtocolVersion, Event: delivery.EventDeliver, Bundle: "pending", HandoffID: "h1"}); err != nil {
		t.Fatal(err)
	}
	conn := &stopFrames{Reader: bytes.NewReader(frames.Bytes())}
	h := hookCall{harness: "codex", a: &app{env: Env{Stdout: failedHookOutput{}, Stderr: io.Discard}}}
	h.waitOn(conn, delivery.Request{Op: delivery.OpWait})
	reader := bufio.NewReader(&conn.writes)
	var request delivery.Request
	if err := delivery.ReadFrame(reader, &request); err != nil {
		t.Fatal(err)
	}
	if err := delivery.ReadFrame(reader, &request); !errors.Is(err, io.EOF) {
		t.Fatalf("failed stdout accepted a handoff: %+v, %v", request, err)
	}
}

func TestStopTracksExactTurnCompletionAndContinuation(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "continued"}[blocked], func(t *testing.T) {
			var frames bytes.Buffer
			if err := delivery.WriteFrame(&frames, delivery.Response{V: 1, Event: delivery.EventWaiting, Boot: "actual-boot", TurnID: 7}); err != nil {
				t.Fatal(err)
			}
			end := delivery.Response{V: 1, Event: delivery.EventRelease}
			if blocked {
				end = delivery.Response{V: 1, Event: delivery.EventDeliver, Bundle: "continue this turn", HandoffID: "h1"}
			}
			if err := delivery.WriteFrame(&frames, end); err != nil {
				t.Fatal(err)
			}
			conn := &stopFrames{Reader: bytes.NewReader(frames.Bytes())}
			var completion delivery.Request
			continued := false
			h := hookCall{harness: "codex", a: &app{env: Env{Stdout: io.Discard, Stderr: io.Discard}}, completion: &completion, continued: &continued}
			started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			h.waitOn(conn, delivery.Request{Op: delivery.OpWait, Harness: "codex", Session: "thread", Started: started})
			if continued != blocked || completion.Op != delivery.OpTurnEnd || completion.TurnID != 7 || completion.Boot != "actual-boot" || completion.Session != "thread" || !completion.Started.Equal(started) {
				t.Fatalf("completion fence: continued=%v request=%+v", continued, completion)
			}
		})
	}
}
