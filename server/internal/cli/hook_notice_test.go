package cli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestClaudeStopFailedOutputDoesNotAcceptHandoff(t *testing.T) {
	var frames bytes.Buffer
	if err := delivery.WriteFrame(&frames, delivery.Response{V: 1, Event: delivery.EventDeliver, Bundle: "pending notice", HandoffID: "hnd_exact"}); err != nil {
		t.Fatal(err)
	}
	conn := &stopFrames{Reader: bytes.NewReader(frames.Bytes())}
	h := hookCall{harness: "claude-code", a: &app{env: Env{Stdout: &bytes.Buffer{}, Stderr: failedHookOutput{}}}}
	h.waitOn(conn, delivery.Request{Op: delivery.OpWait})
	// Only the initial wait request may be written, never a receipt.
	var req delivery.Request
	reader := bufio.NewReader(&conn.writes)
	if err := delivery.ReadFrame(reader, &req); err != nil {
		t.Fatal(err)
	}
	if err := delivery.ReadFrame(reader, &req); !errors.Is(err, io.EOF) {
		t.Fatal("failed output acknowledged handoff")
	}
}
