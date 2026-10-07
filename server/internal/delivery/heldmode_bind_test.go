package delivery_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// heldInbox holds the inbox reads it is armed for until release is closed: before it
// reads the server, or after, so that the answer is older than the server by then.
type heldInbox struct {
	delivery.Server
	armed     atomic.Bool
	readFirst bool
	reading   chan struct{}
	release   chan struct{}
}

func (g *heldInbox) Inbox(ctx context.Context, agent delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	if !g.armed.Swap(false) {
		return g.Server.Inbox(ctx, agent)
	}
	if g.readFirst {
		msgs, cursor, mode, err = g.Server.Inbox(ctx, agent)
	}
	close(g.reading)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, 0, nil, ctx.Err()
	}
	if g.readFirst {
		return msgs, cursor, mode, err
	}
	return g.Server.Inbox(ctx, agent)
}

// A session bound before the daemon read the agent's mode is told of a change its
// person makes then, whether the daemon's first read was on its way when the command
// passed the change on, or went out after it.
func TestAModeChangedBeforeTheFirstReadIsTold(t *testing.T) {
	for name, readFirst := range map[string]bool{"read on its way": true, "read after the change": false} {
		t.Run(name, func(t *testing.T) {
			g := &heldInbox{readFirst: readFirst, reading: make(chan struct{}), release: make(chan struct{})}
			r := newRigWithServer(t, func(base delivery.Server) delivery.Server {
				g.Server = base
				return g
			})
			var release sync.Once
			open := func() { release.Do(func() { close(g.release) }) }
			t.Cleanup(open)
			r.server.HoldModes()
			r.register("s1", "b1")
			g.armed.Store(true)
			r.bind("claude-code", "s1", reviewer)
			<-g.reading

			// The command that sets the mode on the server passes it on, as aboard delivery does.
			rev := r.server.SetHeldMode(reviewer, delivery.ModeAll)
			if got := r.ok(delivery.Request{Op: delivery.OpMode, Agent: &reviewer, Mode: delivery.ModeAll, Revision: int64(rev)}); !got.Changed {
				t.Fatalf("the change wasn't taken: %+v", got)
			}
			open()

			h := r.wait("s1", "b1", false)
			r.post(reviewer, "FYI: the build is green", false)
			b := h.bundle()
			if line := deliverytext.ModeChanged("docs", "focused", "all"); !strings.HasPrefix(b, line) || !strings.Contains(b, "the build is green") {
				t.Fatalf("the bundle should start with the changed-mode line:\n%s", b)
			}
		})
	}
}
