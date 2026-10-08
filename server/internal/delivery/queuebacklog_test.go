package delivery_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestBusyQueueWaitsAndCoalescesOnlyUnreadMessagesAtTurnEnd(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "busy-backlog", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "busy-backlog", reviewer)
	r.ok(req(delivery.OpPrompt))

	postWhileBusy := func(body string) int {
		t.Helper()
		seq := r.post(reviewer, body, false)
		r.eventually("message observed during the busy turn", 0, func() bool {
			return len(r.codex.Handed("busy-backlog")) > 0 ||
				strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", seq))
		})
		r.clock.Advance(2 * delivery.QueueGather)
		r.ok(req(delivery.OpBoundary))
		r.ok(req(delivery.OpBoundary))
		if got := r.codex.Handed("busy-backlog"); len(got) != 0 {
			t.Fatalf("busy-turn messages became separate external queue entries: %q", got)
		}
		return seq
	}

	old := postWhileBusy("already read elsewhere")
	if err := r.server.Ack(context.Background(), reviewer, old); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(2 * time.Hour)
	postWhileBusy("remaining first")
	r.clock.Advance(time.Hour)
	last := postWhileBusy("remaining second")
	r.ok(req(delivery.OpTurnEnd))
	r.eventually("one coalesced turn-end bundle", delivery.QueueGather, func() bool {
		return len(r.codex.Handed("busy-backlog")) > 0
	})
	got := r.codex.Handed("busy-backlog")
	if len(got) != 1 || strings.Contains(got[0], "already read elsewhere") ||
		!strings.Contains(got[0], "remaining first") || !strings.Contains(got[0], "remaining second") {
		t.Fatalf("want one bundle with only both unread messages: %q", got)
	}
	r.eventually("coalesced acknowledgement", 0, func() bool {
		return r.server.Cursor(reviewer) == last
	})
}

func TestCodexStopTakesCombinedBundleWithoutExternalQueue(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "stop-bundle", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "stop-bundle", reviewer)
	r.ok(req(delivery.OpPrompt))
	r.post(reviewer, "first at stop", false)
	r.post(reviewer, "second at stop", false)
	h := r.wait("stop-bundle", "b1", false, "codex")
	got := h.bundle()
	if !strings.Contains(got, "first at stop") || !strings.Contains(got, "second at stop") {
		t.Fatalf("missing combined Stop context: %q", got)
	}
	if queued := r.codex.Handed("stop-bundle"); len(queued) != 0 {
		t.Fatalf("Stop delivery used external queue: %q", queued)
	}
}

type failingRefresh struct {
	delivery.Server
	fail      atomic.Bool
	attempted chan struct{}
}

func (s *failingRefresh) Inbox(ctx context.Context, ref delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	if s.fail.Load() {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
		return nil, 0, nil, errors.New("temporary inbox failure")
	}
	return s.Server.Inbox(ctx, ref)
}

func TestTurnEndDoesNotHandCachedMessagesWhenFreshReadFails(t *testing.T) {
	var remote *failingRefresh
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		remote = &failingRefresh{Server: s, attempted: make(chan struct{}, 20)}
		return remote
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "failed-refresh", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "failed-refresh", reviewer)
	r.ok(req(delivery.OpPrompt))
	seq := r.post(reviewer, "now read elsewhere", false)
	r.eventually("initial message observed", 0, func() bool { return strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", seq)) })
	if err := r.server.Ack(context.Background(), reviewer, seq); err != nil {
		t.Fatal(err)
	}
	remote.fail.Store(true)
	r.ok(req(delivery.OpTurnEnd))
	r.clock.Advance(2 * delivery.QueueGather)
	select {
	case <-remote.attempted:
	case <-time.After(within):
		t.Fatal("fresh read wasn't attempted")
	}
	r.ok(req(delivery.OpAgents))
	if got := r.codex.Handed("failed-refresh"); len(got) != 0 {
		t.Fatalf("failed fresh read handed stale cached text: %q", got)
	}
	remote.fail.Store(false)
	r.clock.Advance(2 * delivery.QueueGather)
	r.ok(req(delivery.OpAgents))
	if got := r.codex.Handed("failed-refresh"); len(got) != 0 {
		t.Fatalf("already-read text was handed after recovery: %q", got)
	}
}

func TestCodexStopOversizedMessageStaysUnreadAndGetsReadHint(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "large-stop", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "large-stop", reviewer)
	r.ok(req(delivery.OpPrompt))
	r.post(reviewer, strings.Repeat("large body ", 1000), false)
	got := r.wait("large-stop", "b1", false, "codex").bundle()
	if len(got) > 2000 || !strings.Contains(got, "aboard inbox") {
		t.Fatalf("Stop must return a bounded read hint: %d bytes %q", len(got), got)
	}
	if r.server.Cursor(reviewer) != 0 || len(r.codex.Handed("large-stop")) != 0 {
		t.Fatal("oversized Stop context was acknowledged or queued externally")
	}
}

func TestBusyCodexTurnDoesNotQueueBacklogAfterDaemonRestart(t *testing.T) {
	r := newRig(t)
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "restart-busy", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "restart-busy", reviewer)
	r.ok(req(delivery.OpPrompt))
	seq := r.post(reviewer, "waiting across daemon restart", false)
	r.eventually("initial message observed", 0, func() bool { return strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", seq)) })
	r.stop()
	r.start()
	r.eventually("restart observed pending message", 0, func() bool {
		return strings.Contains(r.ok(req(delivery.OpBoundary)).Notice, fmt.Sprintf("#%d ", seq)) || len(r.codex.Handed("restart-busy")) > 0
	})
	r.clock.Advance(2 * delivery.QueueGather)
	r.ok(req(delivery.OpAgents))
	if got := r.codex.Handed("restart-busy"); len(got) != 0 {
		t.Fatalf("daemon restart queued a known busy turn: %q", got)
	}
	if got := r.wait("restart-busy", "b1", false, "codex").bundle(); !strings.Contains(got, "waiting across daemon restart") {
		t.Fatalf("Stop lost pending message: %q", got)
	}
}
