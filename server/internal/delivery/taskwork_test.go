package delivery_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

type taskWorkServer struct {
	delivery.Server
	work *deliverytext.TaskWork
}

func (s taskWorkServer) TaskWork(context.Context, delivery.AgentRef) (*deliverytext.TaskWork, error) {
	return s.work, nil
}

func TestRegisterTaskContextDoesNotWakeOrAcknowledge(t *testing.T) {
	work := &deliverytext.TaskWork{Nudges: false, CurrentTask: &deliverytext.TaskContext{TaskRef: deliverytext.TaskRef{Ref: "CHK-17", Title: "Review staging"}, Owner: true}}
	r := newRigWithServer(t, func(server delivery.Server) delivery.Server { return taskWorkServer{Server: server, work: work} })
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	got := r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "s1", Boot: "b1", Source: "compact"})
	if !strings.Contains(got.Note, `CHK-17 (task title: "Review staging"; owner)`) || len(got.Note) > 600 {
		t.Fatalf("register note=%q", got.Note)
	}
	if len(r.claude.Handed("s1")) != 0 || r.server.Cursor(reviewer) != 0 {
		t.Fatal("task context changed delivery or acknowledged")
	}
}

func TestBriefTurnReminderDoesNotWakeAckOrRepeat(t *testing.T) {
	work := &deliverytext.TaskWork{Nudges: true, Brief: &deliverytext.BriefContext{FileID: "fil_keeper", Name: "brief.md", Version: 2, At: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), MessagesSince: 30}}
	r := newRigWithServer(t, func(server delivery.Server) delivery.Server { return taskWorkServer{Server: server, work: work} })
	r.register("ready", "ready-boot")
	r.stop()
	var calls atomic.Int32
	r.configure = func(cfg *delivery.Config) {
		cfg.AllowBriefNudge = func(_ delivery.AgentRef, _ deliverytext.BriefContext) bool { return calls.Add(1) == 1 }
	}
	r.start()
	r.register("s1", "b1")
	r.bind("claude-code", "s1", reviewer)
	first := r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if !strings.HasPrefix(first.Nudge, "Aboard: your brief") || first.Bundle != "" {
		t.Fatalf("turn output=%+v", first)
	}
	second := r.ok(delivery.Request{Op: delivery.OpTurnStart, Harness: "claude-code", Session: "s1", Boot: "b1"})
	if second.Nudge != "" {
		t.Fatal("second turn repeated keeper advice")
	}
	if len(r.claude.Handed("s1")) != 0 || r.server.Cursor(reviewer) != 0 {
		t.Fatal("keeper advice woke or acknowledged")
	}
}
