package delivery_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type reportingServer struct {
	delivery.Server
	reports        chan delivery.QueueReportIntent
	claimKeys      chan string
	loseFirstClaim bool
	claimLost      chan struct{}
}

func (s *reportingServer) QueueFence(context.Context, delivery.AgentRef) (delivery.QueueFence, error) {
	return delivery.QueueFence{BoardID: "brd_docs", MemberID: reviewer.MemberID, CredentialGeneration: reviewer.MemberID}, nil
}

func (s *reportingServer) ReportQueue(ctx context.Context, ref delivery.AgentRef, in delivery.QueueReportIntent) (delivery.QueueFence, error) {
	epoch, revision := in.Epoch, in.Revision
	if in.ExpectedEpoch != nil {
		epoch, revision = *in.ExpectedEpoch+1, 0
		if s.claimKeys != nil {
			select {
			case s.claimKeys <- in.IdempotencyKey:
			case <-ctx.Done():
				return delivery.QueueFence{}, ctx.Err()
			}
		}
		if s.loseFirstClaim {
			s.loseFirstClaim = false
			close(s.claimLost)
			return delivery.QueueFence{}, io.ErrUnexpectedEOF
		}
	} else {
		select {
		case s.reports <- in:
		case <-ctx.Done():
			return delivery.QueueFence{}, ctx.Err()
		}
	}
	return delivery.QueueFence{BoardID: "brd_docs", MemberID: ref.MemberID, Epoch: epoch, Revision: revision, CredentialGeneration: reviewer.MemberID}, nil
}

func (s *reportingServer) QueuedMessage(ctx context.Context, ref delivery.AgentRef, id string, seq int) (delivery.Message, error) {
	reader, ok := s.Server.(delivery.QueuedMessageServer)
	if !ok {
		return delivery.Message{}, delivery.ErrUnauthorized
	}
	return reader.QueuedMessage(ctx, ref, id, seq)
}

func TestBusyQueueReceiptsPublishAndShownReadClearsThem(t *testing.T) {
	var remote *reportingServer
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		remote = &reportingServer{Server: s, reports: make(chan delivery.QueueReportIntent, 20)}
		return remote
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "queue-publisher", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "queue-publisher", reviewer)
	r.ok(req(delivery.OpPrompt))
	seq := r.server.Post(reviewer, delivery.Message{ID: "msg_waiting", BoardID: "brd_docs", Body: "waiting for end"})
	wait := func(count int) delivery.QueueReportIntent {
		t.Helper()
		deadline := time.NewTimer(within)
		defer deadline.Stop()
		for {
			select {
			case in := <-remote.reports:
				if len(in.Messages) == count {
					return in
				}
			case <-deadline.C:
				t.Fatalf("no queue report with %d identities", count)
				return delivery.QueueReportIntent{}
			}
		}
	}
	queued := wait(1)
	if queued.Messages[0].MessageID != "msg_waiting" || queued.Messages[0].Seq != seq || queued.IdempotencyKey == "" {
		t.Fatalf("report: %+v", queued)
	}
	bindings, err := r.journal.Bindings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	shown := req(delivery.OpShown)
	shown.Agent = &reviewer
	for _, b := range bindings {
		if b.Agent.Key() == reviewer.Key() {
			shown.Generation = b.Generation
		}
	}
	shown.ShownMessages = []delivery.ShownMessage{{BoardID: "brd_docs", MemberID: reviewer.MemberID, MessageID: "msg_waiting", Seq: seq}}
	r.ok(shown)
	cleared := wait(0)
	if cleared.Revision <= queued.Revision {
		t.Fatal("clear did not advance revision")
	}
	if r.server.Cursor(reviewer) != 0 {
		t.Fatal("observational queue report acknowledged message")
	}
}

type reportingPeerServer struct{ *reportingServer }

func (s *reportingPeerServer) Inbox(ctx context.Context, ref delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	msgs, cursor, mode, err = s.Server.Inbox(ctx, ref)
	if mode == nil {
		mode = &delivery.HeldMode{Mode: delivery.ModeFocused}
	} else {
		modeCopy := *mode
		mode = &modeCopy
	}
	mode.MidturnPolicy = "my-agents"
	return msgs, cursor, mode, err
}

func TestHookPeerCandidatesAreNotReportedAsTurnEndQueued(t *testing.T) {
	var remote *reportingPeerServer
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		remote = &reportingPeerServer{reportingServer: &reportingServer{Server: s, reports: make(chan delivery.QueueReportIntent, 20)}}
		return remote
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "hook-queue", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "hook-queue", reviewer)
	r.ok(req(delivery.OpPrompt))
	boundary := req(delivery.OpBoundary)
	boundary.Capabilities = []string{"tool-boundary", "midturn-peer"}
	r.ok(boundary)
	ordinary := r.server.Post(reviewer, delivery.Message{ID: "msg_ordinary", BoardID: "brd_docs", Body: "ordinary peer"})
	r.server.Post(reviewer, delivery.Message{ID: "msg_next_step", BoardID: "brd_docs", Urgent: true, MidturnPeerSenderID: "mem_peer", Sender: "owner_agent", FromName: "peer", To: []string{"@" + reviewer.Name}, Body: "urgent peer"})
	preview := r.ok(req(delivery.OpQueued)).Queued
	if preview == nil || preview.Count != 1 || preview.Messages[0].Seq != ordinary {
		t.Fatalf("next-step peer labeled turn end: %+v", preview)
	}
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	for {
		select {
		case in := <-remote.reports:
			for _, m := range in.Messages {
				if m.MessageID == "msg_next_step" {
					t.Fatal("hook-capable peer reported turn_end")
				}
			}
			if len(in.Messages) == 1 && in.Messages[0].Seq == ordinary {
				return
			}
		case <-deadline.C:
			t.Fatal("ordinary queue observation missing")
		}
	}
}

func TestQueueClaimLostResponseRestartsWithItsRetainedKey(t *testing.T) {
	var remote *reportingServer
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server {
		remote = &reportingServer{Server: s, reports: make(chan delivery.QueueReportIntent, 20), claimKeys: make(chan string, 10), loseFirstClaim: true, claimLost: make(chan struct{})}
		return remote
	})
	req := func(op string) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "lost-claim", Boot: "b1"}
	}
	r.ok(req(delivery.OpRegister))
	r.bind("codex", "lost-claim", reviewer)
	r.ok(req(delivery.OpPrompt))
	r.server.Post(reviewer, delivery.Message{ID: "msg_after_restart", BoardID: "brd_docs", Body: "waiting"})
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	select {
	case <-remote.claimLost:
	case <-deadline.C:
		t.Fatal("claim did not reach server")
	}
	first := <-remote.claimKeys
	r.stop()
	r.start()
	r.ok(req(delivery.OpRegister))
	r.ok(req(delivery.OpPrompt))
	select {
	case next := <-remote.claimKeys:
		if next != first {
			t.Fatal("restart allocated another claim key")
		}
	case <-deadline.C:
		t.Fatal("retained claim was not retried")
	}
	for {
		select {
		case in := <-remote.reports:
			if len(in.Messages) == 1 && in.Messages[0].MessageID == "msg_after_restart" {
				return
			}
		case <-deadline.C:
			t.Fatal("queue publication did not recover")
		}
	}
}
