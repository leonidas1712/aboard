package deliverytest

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// ServerFixture is one Aboard server, prepared for the server contract suite, with two
// agents on one board.
type ServerFixture struct {
	// New returns the server and an agent that receives messages from a peer, and a
	// function that posts a message from the peer to it and returns its sequence number.
	New func(t *testing.T) (srv delivery.Server, to delivery.AgentRef, post func(body string, urgent bool) int)
	// WithModes returns a server that holds delivery modes, an agent on it, and a
	// function that sets the agent's mode as its person would and returns the sequence
	// number of the event that set it.
	WithModes func(t *testing.T) (srv delivery.Server, to delivery.AgentRef, set func(delivery.Mode) int)
}

// streamWait bounds how long the suite waits for a head on the stream.
const streamWait = 10 * time.Second

// RunServer runs the Server contract suite.
func RunServer(t *testing.T, f ServerFixture) {
	ctx := context.Background()

	t.Run("InboxReturnsUnreadMessagesOldestFirst", func(t *testing.T) {
		srv, to, post := f.New(t)
		first := post("first", false)
		second := post("second", true)
		msgs, _, _, err := srv.Inbox(ctx, to)
		must(t, err)
		if len(msgs) != 2 || msgs[0].Seq != first || msgs[1].Seq != second || msgs[0].Body != "first" ||
			msgs[0].Urgent || !msgs[1].Urgent || msgs[0].Board != to.Board || msgs[0].Sender != "owner_agent" {
			t.Fatalf("inbox %+v", msgs)
		}
	})

	t.Run("AckMovesTheReadPositionForwardOnly", func(t *testing.T) {
		srv, to, post := f.New(t)
		first := post("first", false)
		second := post("second", false)
		must(t, srv.Ack(ctx, to, first))
		msgs, cursor, _, err := srv.Inbox(ctx, to)
		must(t, err)
		if cursor != first || len(msgs) != 1 || msgs[0].Seq != second {
			t.Fatalf("after ack %d: cursor %d, inbox %+v", first, cursor, msgs)
		}
		must(t, srv.Ack(ctx, to, second))
		must(t, srv.Ack(ctx, to, first))
		msgs, cursor, _, err = srv.Inbox(ctx, to)
		must(t, err)
		if cursor != second || len(msgs) != 0 {
			t.Fatalf("a lower ack moved the read position back: cursor %d, inbox %+v", cursor, msgs)
		}
	})

	t.Run("InboxCarriesTheModeTheServerHolds", func(t *testing.T) {
		srv, to, set := f.WithModes(t)
		_, _, mode, err := srv.Inbox(ctx, to)
		must(t, err)
		if mode == nil || *mode != (delivery.HeldMode{Mode: delivery.ModeFocused}) {
			t.Fatalf("an agent whose mode was never set: %+v, want focused at revision 0", mode)
		}
		first := set(delivery.ModeHumans)
		second := set(delivery.ModeOff)
		_, _, mode, err = srv.Inbox(ctx, to)
		must(t, err)
		if first <= 0 || second <= first || mode == nil || *mode != (delivery.HeldMode{Mode: delivery.ModeOff, Revision: int64(second)}) {
			t.Fatalf("after setting humans (%d) then off (%d): %+v", first, second, mode)
		}
	})

	t.Run("SetPresenceTakesEveryPresenceTheDaemonReports", func(t *testing.T) {
		srv, to, _ := f.New(t)
		for _, p := range []delivery.Presence{delivery.PresenceIdle, delivery.PresenceWorking, delivery.PresenceWorking, delivery.PresenceNoSession} {
			must(t, srv.SetPresence(ctx, to, p, delivery.ModeAuto))
		}
	})

	t.Run("FollowReportsHeadChanges", func(t *testing.T) {
		srv, to, post := f.New(t)
		fctx, cancel := context.WithCancel(ctx)
		connected := make(chan struct{})
		heads := make(chan delivery.Head, 64)
		done := make(chan error, 1)
		go func() {
			done <- srv.Follow(fctx, func() { close(connected) }, func(h delivery.Head) { heads <- h })
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		select {
		case <-connected:
		case err := <-done:
			t.Fatalf("Follow ended before connecting: %v", err)
		case <-time.After(streamWait):
			t.Fatal("Follow didn't connect")
		}
		seq := post("hello", false)
		deadline := time.After(streamWait)
		var seen []delivery.Head
		for {
			select {
			case h := <-heads:
				seen = append(seen, h)
				if h.Board == to.Board && h.Seq >= seq {
					return
				}
			case <-deadline:
				t.Fatalf("no head for %s at %d; saw %+v", to.Board, seq, seen)
			}
		}
	})

	t.Run("FollowReportsReadPositions", func(t *testing.T) {
		srv, to, post := f.New(t)
		first := post("first", false)
		post("second", false)
		fctx, cancel := context.WithCancel(ctx)
		connected := make(chan struct{})
		heads := make(chan delivery.Head, 64)
		done := make(chan error, 1)
		go func() {
			done <- srv.Follow(fctx, func() { close(connected) }, func(h delivery.Head) { heads <- h })
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
		select {
		case <-connected:
		case <-time.After(streamWait):
			t.Fatal("Follow didn't connect")
		}
		// Any client's acknowledgement moves the read position; this one is the port's.
		must(t, srv.Ack(ctx, to, first))
		deadline := time.After(streamWait)
		var seen []delivery.Head
		for {
			select {
			case h := <-heads:
				seen = append(seen, h)
				if h.Read != nil && h.Board == to.Board && h.Read.Agent == to.Name && h.Read.UpTo == first {
					return
				}
			case <-deadline:
				t.Fatalf("no read position %d for %s; saw %+v", first, to.Name, seen)
			}
		}
	})

	t.Run("FollowEndsWhenTheContextEnds", func(t *testing.T) {
		srv, _, _ := f.New(t)
		fctx, cancel := context.WithCancel(ctx)
		connected := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- srv.Follow(fctx, func() { close(connected) }, func(delivery.Head) {}) }()
		select {
		case <-connected:
		case <-time.After(streamWait):
			t.Fatal("Follow didn't connect")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(streamWait):
			t.Fatal("Follow kept running after its context ended")
		}
	})
}
