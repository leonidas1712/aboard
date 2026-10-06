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

	t.Run("HandoffGenerationAndFrozenEvidence", func(t *testing.T) {
		j := open(t)
		agent := journalSeat(t, writer, "mem_manifest")
		b, err := j.BindGeneration(ctx, delivery.Binding{Agent: agent, Session: claudeA, BoundAt: t0}, false)
		must(t, err)
		must(t, j.SaveSession(ctx, delivery.SessionRecord{Key: claudeA, Boot: "boot", Open: true, UpdatedAt: t0}))
		manifest := delivery.HandoffManifest{
			ID: "hnd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Session: claudeA, Boot: "boot",
			Class: delivery.ClassMixed, PayloadHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: t0,
			Parts: []delivery.HandoffPart{{Agent: agent, Generation: b.Generation, Seqs: []int{1, 2}}},
		}
		saved, err := j.PrepareHandoff(ctx, manifest)
		must(t, err)
		if saved.Parts[0].DeliveryID == 0 {
			t.Fatal("prepared handoff has no durable delivery")
		}
		confirmed, err := j.ConfirmHandoff(ctx, saved.ID, claudeA, "boot", []delivery.AgentKey{agent.Key()}, t0)
		must(t, err)
		if len(confirmed) != 1 || !slices.Equal(confirmed[0].Seqs, []int{1, 2}) {
			t.Fatalf("confirmed manifest: %+v", confirmed)
		}
		rotated, err := j.BindGeneration(ctx, b, true)
		must(t, err)
		if rotated.Generation <= b.Generation {
			t.Fatal("credential change reused generation")
		}
		durable, err := j.Deliveries(ctx, delivery.StateConfirmed)
		must(t, err)
		if len(durable) != 1 {
			t.Fatal("rotation erased received evidence")
		}
	})

	t.Run("SeatIdentitySurvivesRenameAndSeparatesReusedNames", func(t *testing.T) {
		j := open(t)
		a := journalSeat(t, review, "mem_first")
		renamed := a
		renamed.Name = "renamed"
		must(t, j.Bind(ctx, delivery.Binding{Agent: a, Session: claudeA, BoundAt: t0}))
		must(t, j.SetMode(ctx, a, delivery.ModeHumans))
		must(t, j.Bind(ctx, delivery.Binding{Agent: renamed, Session: claudeB, BoundAt: t0}))
		must(t, j.SetMode(ctx, renamed, delivery.ModeOff))
		bindings, err := j.Bindings(ctx)
		must(t, err)
		modes, err := j.Modes(ctx)
		must(t, err)
		if len(bindings) != 1 || bindings[0].Agent != renamed || len(modes) != 1 || modes[renamed] != delivery.ModeOff {
			t.Fatalf("rename duplicated seat state: bindings=%+v modes=%v", bindings, modes)
		}
		b := journalSeat(t, review, "mem_second")
		must(t, j.Bind(ctx, delivery.Binding{Agent: b, Session: claudeA, BoundAt: t0}))
		must(t, j.SetMode(ctx, b, delivery.ModeFocused))
		otherServer := a
		otherServer.Server = "https://another.example"
		must(t, j.SetMode(ctx, otherServer, delivery.ModeAll))
		modes, err = j.Modes(ctx)
		must(t, err)
		bindings, err = j.Bindings(ctx)
		must(t, err)
		if len(bindings) != 2 || len(modes) != 3 || modes[renamed] != delivery.ModeOff || modes[b] != delivery.ModeFocused || modes[otherServer] != delivery.ModeAll {
			t.Fatalf("different seats shared state: bindings=%+v modes=%v", bindings, modes)
		}
	})

	t.Run("SameNameAndIdOnAnotherServerNeverShareState", func(t *testing.T) {
		j := open(t)
		old := journalSeat(t, review, "mem_old")
		fresh := journalSeat(t, review, "mem_new")
		other := old
		other.Server = "https://another.example"
		third := delivery.SessionKey{Harness: "codex", ID: "s-third"}
		for _, b := range []delivery.Binding{{Agent: old, Session: claudeA, BoundAt: t0}, {Agent: fresh, Session: claudeB, BoundAt: t0}, {Agent: other, Session: third, BoundAt: t0}} {
			must(t, j.Bind(ctx, b))
		}
		must(t, j.SetMode(ctx, old, delivery.ModeOff))
		must(t, j.SetMode(ctx, fresh, delivery.ModeFocused))
		must(t, j.SetMode(ctx, other, delivery.ModeHumans))
		_, err := j.AddDelivery(ctx, delivery.Delivery{Agent: old, Session: claudeA, State: delivery.StateConfirmed, Seqs: []int{6}, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
		bindings, err := j.Bindings(ctx)
		must(t, err)
		modes, err := j.Modes(ctx)
		must(t, err)
		ds, err := j.Deliveries(ctx, delivery.StateConfirmed)
		must(t, err)
		if len(bindings) != 3 || len(modes) != 3 || modes[old] != delivery.ModeOff || modes[fresh] != delivery.ModeFocused || modes[other] != delivery.ModeHumans || len(ds) != 1 || ds[0].Agent != old {
			t.Fatalf("seat state leaked: bindings=%+v modes=%v deliveries=%+v", bindings, modes, ds)
		}
	})

	t.Run("ResolveIdentityPromotesOnlyVerifiedLegacyState", func(t *testing.T) {
		j := open(t)
		resolved := journalSeat(t, review, "mem_verified")
		unrelated := journalSeat(t, review, "mem_unrelated")
		must(t, j.Bind(ctx, delivery.Binding{Agent: review, Session: claudeA, BoundAt: t0}))
		must(t, j.SetMode(ctx, review, delivery.ModeHumans))
		must(t, j.SetMode(ctx, unrelated, delivery.ModeOff))
		must(t, j.SaveSession(ctx, delivery.SessionRecord{Key: claudeA, Boot: "old", Lost: &review, UpdatedAt: t0}))
		_, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, Boot: "old", State: delivery.StateConfirmed, Seqs: []int{6, 7}, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
		must(t, j.ResolveIdentity(ctx, review, resolved))
		must(t, j.ResolveIdentity(ctx, review, resolved))
		bindings, err := j.Bindings(ctx)
		must(t, err)
		modes, err := j.Modes(ctx)
		must(t, err)
		sessions, err := j.Sessions(ctx)
		must(t, err)
		ds, err := j.Deliveries(ctx, delivery.StateConfirmed)
		must(t, err)
		if len(bindings) != 1 || bindings[0].Agent != resolved || modes[resolved] != delivery.ModeHumans || modes[unrelated] != delivery.ModeOff ||
			len(modes) != 2 || sessions[0].Lost == nil || *sessions[0].Lost != resolved || len(ds) != 1 || ds[0].Agent != resolved || !slices.Equal(ds[0].Seqs, []int{6, 7}) {
			t.Fatalf("resolved legacy state: bindings=%+v modes=%v sessions=%+v deliveries=%+v", bindings, modes, sessions, ds)
		}
	})

	t.Run("ResolveIdentityConflictRollsBackEveryRow", func(t *testing.T) {
		j := open(t)
		resolved := journalSeat(t, review, "mem_verified")
		must(t, j.Bind(ctx, delivery.Binding{Agent: review, Session: claudeA, BoundAt: t0}))
		must(t, j.SetMode(ctx, review, delivery.ModeHumans))
		must(t, j.SetMode(ctx, resolved, delivery.ModeOff))
		_, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, State: delivery.StateConfirmed, Seqs: []int{6}, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
		if err := j.ResolveIdentity(ctx, review, resolved); err == nil {
			t.Fatal("conflicting verified mode was overwritten")
		}
		bindings, err := j.Bindings(ctx)
		must(t, err)
		ds, err := j.Deliveries(ctx, delivery.StateConfirmed)
		must(t, err)
		modes, err := j.Modes(ctx)
		must(t, err)
		if len(bindings) != 1 || bindings[0].Agent != review || len(ds) != 1 || ds[0].Agent != review || modes[review] != delivery.ModeHumans || modes[resolved] != delivery.ModeOff {
			t.Fatalf("conflict partially promoted state: bindings=%+v modes=%v deliveries=%+v", bindings, modes, ds)
		}
		wrongServer := resolved
		wrongServer.Server = "https://another.example"
		if err := j.ResolveIdentity(ctx, review, wrongServer); err == nil {
			t.Fatal("cross-server resolution succeeded")
		}
		if err := j.ResolveIdentity(ctx, resolved, journalSeat(t, review, "mem_other")); err == nil {
			t.Fatal("an already verified seat was reassigned")
		}
	})

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

	t.Run("UpdateKeepsHowFarADeliveryGot", func(t *testing.T) {
		j := open(t)
		id, err := j.AddDelivery(ctx, delivery.Delivery{Agent: review, Session: claudeA, State: delivery.StateHanded, Seqs: []int{6}, CreatedAt: t0, UpdatedAt: t0})
		must(t, err)
		accepted, started := t0.Add(time.Second), t0.Add(2*time.Second)
		must(t, j.UpdateDelivery(ctx, delivery.Delivery{
			ID: id, Agent: review, Session: claudeA, State: delivery.StateConfirmed, UpdatedAt: started,
			AcceptedAt: accepted, TurnStartedAt: started, Stalled: true,
		}))
		got, err := j.Deliveries(ctx, delivery.StateConfirmed)
		must(t, err)
		if len(got) != 1 || !got[0].AcceptedAt.Equal(accepted) || !got[0].TurnStartedAt.Equal(started) || !got[0].Stalled {
			t.Fatalf("got %+v", got)
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

func journalSeat(t *testing.T, ref delivery.AgentRef, id string) delivery.AgentRef {
	t.Helper()
	ref.MemberID = id
	return ref
}
