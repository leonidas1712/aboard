package delivery_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestFailedAdmissionReadsShareBackoffAcrossHooks(t *testing.T) {
	var remote *failingRefresh
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		remote = &failingRefresh{Server: s, attempted: make(chan struct{}, 100)}
		return remote
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "read-overload", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "read-overload", reviewer)
	r.ok(req(delivery.OpPrompt))
	seq := r.post(reviewer, "retained while overloaded", false)
	r.eventually("cached unread message", 0, func() bool { return strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", seq)) })
	remote.fail.Store(true)
	for attempt := 0; attempt < 7; attempt++ {
		r.ok(req(delivery.OpTurnStart))
		select {
		case <-remote.attempted:
		default:
			t.Fatal("due admission read was not attempted")
		}
		for _, op := range []string{delivery.OpBoundary, delivery.OpTurnStart} {
			got := r.ok(req(op))
			if got.Bundle != "" {
				t.Fatalf("failed read handed cached text: %q", got.Bundle)
			}
		}
		if len(remote.attempted) != 0 {
			t.Fatalf("hooks bypassed failed-read backoff on attempt %d", attempt+1)
		}
		if r.server.Cursor(reviewer) != 0 || r.codex.Attempts() != 0 {
			t.Fatal("read failure acknowledged or handed the message")
		}
		r.clock.Advance(time.Minute)
	}
	remote.fail.Store(false)
	got := r.ok(req(delivery.OpTurnStart))
	if !strings.Contains(got.Bundle, "retained while overloaded") {
		t.Fatalf("recovery lost unread text: %+v", got)
	}
	r.ok(req(delivery.OpPrompt))
	next := r.post(reviewer, "after recovery", false)
	r.eventually("new message cached", 0, func() bool { return strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", next)) })
	remote.fail.Store(true)
	r.ok(req(delivery.OpTurnStart))
	select {
	case <-remote.attempted:
	default:
		t.Fatal("successful read did not reset failure admission")
	}
	r.clock.Advance(delivery.QueueGather)
	r.ok(req(delivery.OpTurnStart))
	select {
	case <-remote.attempted:
	default:
		t.Fatal("recovery retained the minute-long backoff")
	}
}
