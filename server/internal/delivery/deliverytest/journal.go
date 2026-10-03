// Package deliverytest holds the contract suites every implementation of a delivery
// port must pass, and fakes of the outside world (a harness, a server, the control
// socket) for testing the daemon without running Claude Code, Codex or a server.
package deliverytest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

var (
	t0      = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	writer  = delivery.AgentRef{Server: "http://127.0.0.1:7400", Board: "docs", Name: "writer"}
	review  = delivery.AgentRef{Server: "http://127.0.0.1:7400", Board: "docs", Name: "reviewer"}
	claudeA = delivery.SessionKey{Harness: "claude-code", ID: "s-a"}
	claudeB = delivery.SessionKey{Harness: "claude-code", ID: "s-b"}
)

// RunJournal runs the Journal contract suite. open returns a new, empty journal.
func RunJournal(t *testing.T, open func(t *testing.T) delivery.Journal) {
	ctx := context.Background()

	t.Run("SessionsAreSavedAndUpdated", func(t *testing.T) {
		j := open(t)
		must(t, j.SaveSession(ctx, delivery.SessionRecord{Key: claudeA, Boot: "b1", Open: true, UpdatedAt: t0}))
		must(t, j.SaveSession(ctx, delivery.SessionRecord{Key: claudeA, Boot: "b2", Open: false, Lost: &review, UpdatedAt: t0.Add(time.Minute)}))
		must(t, j.SaveSession(ctx, delivery.SessionRecord{Key: claudeB, Boot: "c1", Open: true, Process: &delivery.Process{PID: 4182, Start: 1759320000}, UpdatedAt: t0}))
		got, err := j.Sessions(ctx)
		must(t, err)
		want := []delivery.SessionRecord{
			{Key: claudeA, Boot: "b2", Open: false, Lost: &review, UpdatedAt: t0.Add(time.Minute)},
			{Key: claudeB, Boot: "c1", Open: true, Process: &delivery.Process{PID: 4182, Start: 1759320000}, UpdatedAt: t0},
		}
		if !slices.EqualFunc(got, want, sameSession) {
			t.Fatalf("sessions:\n got %+v\nwant %+v", got, want)
		}
	})

	t.Run("BindingMovesAnAgentToTheNewSession", func(t *testing.T) {
		j := open(t)
		must(t, j.Bind(ctx, delivery.Binding{Agent: review, Session: claudeA, BoundAt: t0}))
		must(t, j.Bind(ctx, delivery.Binding{Agent: writer, Session: claudeA, BoundAt: t0}))
		must(t, j.Bind(ctx, delivery.Binding{Agent: review, Session: claudeB, BoundAt: t0.Add(time.Second)}))
		got, err := j.Bindings(ctx)
		must(t, err)
		if len(got) != 2 {
			t.Fatalf("want one binding per agent, got %+v", got)
		}
		for _, b := range got {
			want := claudeA
			if b.Agent == review {
				want = claudeB
			}
			if b.Session != want {
				t.Errorf("%s bound to %v, want %v", b.Agent.Name, b.Session, want)
			}
		}
	})

	t.Run("ASessionHoldsOneBinding", func(t *testing.T) {
		j := open(t)
		must(t, j.Bind(ctx, delivery.Binding{Agent: review, Session: claudeA, BoundAt: t0}))
		must(t, j.Bind(ctx, delivery.Binding{Agent: writer, Session: claudeA, BoundAt: t0.Add(time.Second)}))
		got, err := j.Bindings(ctx)
		must(t, err)
		if len(got) != 1 || got[0].Agent != writer || got[0].Session != claudeA {
			t.Fatalf("want only the latest agent bound to the session, got %+v", got)
		}
	})

	t.Run("ModesAreSavedAndReplaced", func(t *testing.T) {
		j := open(t)
		got, err := j.Modes(ctx)
		must(t, err)
		if len(got) != 0 {
			t.Fatalf("a new journal has modes: %v", got)
		}
		must(t, j.SetMode(ctx, review, delivery.ModeHumans))
		must(t, j.SetMode(ctx, writer, delivery.ModeOff))
		must(t, j.SetMode(ctx, review, delivery.ModeAuto))
		got, err = j.Modes(ctx)
		must(t, err)
		if len(got) != 2 || got[review] != delivery.ModeAuto || got[writer] != delivery.ModeOff {
			t.Fatalf("modes: %v", got)
		}
	})

	t.Run("DeliveriesKeepTheirMessagesInOrder", func(t *testing.T) {
		j := open(t)
		d := delivery.Delivery{Agent: review, Session: claudeA, Boot: "b1", State: delivery.StateHanded, Seqs: []int{9, 6, 7}, CreatedAt: t0, UpdatedAt: t0}
		id, err := j.AddDelivery(ctx, d)
		must(t, err)
		got, err := j.Deliveries(ctx, delivery.StateHanded)
		must(t, err)
		if len(got) != 1 || got[0].ID != id || !slices.Equal(got[0].Seqs, []int{6, 7, 9}) || got[0].Agent != review ||
			got[0].Session != claudeA || got[0].Boot != "b1" || !got[0].CreatedAt.Equal(t0) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("UpdateChangesStateAndKeepsMessages", func(t *testing.T) {
		j := open(t)
		id, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, State: delivery.StateHanded, Seqs: []int{6}, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
		retry := t0.Add(4 * time.Second)
		must(t, j.UpdateDelivery(ctx, delivery.Delivery{
			ID: id, Agent: review, Session: claudeB, Boot: "b9", State: delivery.StateRetry, Seqs: []int{99},
			Attempts: 3, Reason: delivery.ReasonHarnessError, RetryAt: retry, UpdatedAt: t0.Add(time.Second),
		}))
		got, err := j.Deliveries(ctx, delivery.StateRetry)
		must(t, err)
		if len(got) != 1 {
			t.Fatalf("got %+v", got)
		}
		d := got[0]
		if d.Session != claudeB || d.Boot != "b9" || d.Attempts != 3 || d.Reason != delivery.ReasonHarnessError ||
			!d.RetryAt.Equal(retry) || !slices.Equal(d.Seqs, []int{6}) {
			t.Fatalf("after update: %+v", d)
		}
		if left, _ := j.Deliveries(ctx, delivery.StateHanded); len(left) != 0 {
			t.Fatalf("still handed: %+v", left)
		}
	})

	t.Run("DeliveriesFiltersByStateOldestFirst", func(t *testing.T) {
		j := open(t)
		var ids []int64
		for i, st := range []delivery.State{delivery.StateDone, delivery.StateAttention, delivery.StateSkipped, delivery.StatePending} {
			id, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, State: st, Seqs: []int{i + 1}, CreatedAt: t0, UpdatedAt: t0})
			must(t, err)
			ids = append(ids, id)
		}
		got, err := j.Deliveries(ctx, delivery.StatePending, delivery.StateAttention, delivery.StateSkipped)
		must(t, err)
		var gotIDs []int64
		for _, d := range got {
			gotIDs = append(gotIDs, d.ID)
		}
		if !slices.Equal(gotIDs, ids[1:]) {
			t.Fatalf("ids %v, want %v", gotIDs, ids[1:])
		}
		if none, _ := j.Deliveries(ctx); len(none) != 0 {
			t.Fatalf("no states asked, got %+v", none)
		}
	})

	t.Run("ADeliveryNeedsMessages", func(t *testing.T) {
		j := open(t)
		if _, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, State: delivery.StateHanded, CreatedAt: t0, UpdatedAt: t0}); err == nil {
			t.Fatal("a delivery without messages was recorded")
		}
	})

	t.Run("UpdatingAnUnknownDeliveryFails", func(t *testing.T) {
		j := open(t)
		if err := j.UpdateDelivery(ctx, delivery.Delivery{ID: 4242, State: delivery.StateDone}); err == nil {
			t.Fatal("updating a delivery that doesn't exist succeeded")
		}
	})
}

func sameSession(a, b delivery.SessionRecord) bool {
	return a.Key == b.Key && a.Boot == b.Boot && a.Open == b.Open && a.UpdatedAt.Equal(b.UpdatedAt) &&
		(a.Process == nil) == (b.Process == nil) && (a.Process == nil || *a.Process == *b.Process) &&
		(a.Lost == nil) == (b.Lost == nil) && (a.Lost == nil || *a.Lost == *b.Lost)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
