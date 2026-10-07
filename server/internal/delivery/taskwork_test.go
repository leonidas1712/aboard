package delivery_test

import (
	"context"
	"strings"
	"testing"

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
	if !strings.Contains(got.Note, "CHK-17 Review staging (owner)") || len(got.Note) > 600 {
		t.Fatalf("register note=%q", got.Note)
	}
	if len(r.claude.Handed("s1")) != 0 || r.server.Cursor(reviewer) != 0 {
		t.Fatal("task context changed delivery or acknowledged")
	}
}
