package sqlitejournal

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func handoffFixture(t *testing.T) (*Journal, delivery.HandoffManifest) {
	t.Helper()
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	session := delivery.SessionKey{Harness: "codex", ID: "session"}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: session, Boot: "boot", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	m := delivery.HandoffManifest{ID: "hnd_0123456789abcdef0123456789abcdef", Session: session, Boot: "boot", Class: delivery.ClassMixed, PayloadHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", CreatedAt: now}
	for _, board := range []string{"one", "two"} {
		a := delivery.AgentRef{Server: "https://team.example", Board: board, Name: "writer", MemberID: "mem_" + board}
		b, err := j.BindGeneration(ctx, delivery.Binding{RetainSiblings: true, Agent: a, Session: session, BoundAt: now}, false)
		if err != nil {
			t.Fatal(err)
		}
		m.Parts = append(m.Parts, delivery.HandoffPart{Agent: a, Generation: b.Generation, Seqs: []int{1, 2}})
	}
	return j, m
}

func TestHandoffAtomicPreparationAndImmutableRetry(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	if _, err := j.db.ExecContext(ctx, `CREATE TRIGGER fail_second BEFORE INSERT ON delivery_messages WHEN NEW.delivery_id = 2 BEGIN SELECT RAISE(ABORT,'failed second part'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("partial preparation succeeded")
	}
	for _, table := range []string{"deliveries", "handoffs"} {
		var count int
		if err := j.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s: %d %v", table, count, err)
		}
	}
	if _, err := j.db.ExecContext(ctx, "DROP TRIGGER fail_second"); err != nil {
		t.Fatal(err)
	}
	saved, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := j.PrepareHandoff(ctx, m)
	if err != nil || !reflect.DeepEqual(saved, retry) {
		t.Fatalf("retry changed manifest: %+v %v", retry, err)
	}
	altered := m
	altered.PayloadHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := j.PrepareHandoff(ctx, altered); err == nil {
		t.Fatal("same id changed payload")
	}
	altered = m
	altered.Parts = append([]delivery.HandoffPart(nil), m.Parts...)
	altered.Parts[0], altered.Parts[1] = altered.Parts[1], altered.Parts[0]
	if _, err := j.PrepareHandoff(ctx, altered); err == nil {
		t.Fatal("same id changed allocation order")
	}
	ds, err := j.Deliveries(ctx, delivery.StateHanded)
	if err != nil || len(ds) != 2 || ds[0].HandoffID != m.ID || ds[1].HandoffID != m.ID {
		t.Fatalf("handed rows %+v %v", ds, err)
	}
	manifests, err := j.Handoffs(ctx)
	if err != nil || len(manifests) != 1 || !reflect.DeepEqual(saved, manifests[0]) {
		t.Fatalf("manifest recovery %+v %v", manifests, err)
	}
}

func TestHandoffConfirmationIsAtomicAndFenced(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	m, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	keys := []delivery.AgentKey{m.Parts[0].Agent.Key(), m.Parts[1].Agent.Key()}
	if _, err := j.db.ExecContext(ctx, `CREATE TRIGGER fail_confirm BEFORE UPDATE OF state ON deliveries WHEN NEW.id = 2 AND NEW.state = 'confirmed' BEGIN SELECT RAISE(ABORT,'failed confirmation'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.ConfirmHandoff(ctx, m.ID, m.Session, m.Boot, keys, m.CreatedAt); err == nil {
		t.Fatal("partial confirmation succeeded")
	}
	ds, err := j.Deliveries(ctx, delivery.StateConfirmed)
	if err != nil || len(ds) != 0 {
		t.Fatalf("partial confirmation %+v %v", ds, err)
	}
	if _, err := j.db.ExecContext(ctx, "DROP TRIGGER fail_confirm"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.db.ExecContext(ctx, `UPDATE bindings SET generation = generation + 1 WHERE member_id = ?`, m.Parts[1].Agent.MemberID); err != nil {
		t.Fatal(err)
	}
	ds, err = j.ConfirmHandoff(ctx, m.ID, m.Session, m.Boot, keys, m.CreatedAt)
	if err != nil || len(ds) != 1 || ds[0].Agent.MemberID != m.Parts[0].Agent.MemberID {
		t.Fatalf("stale generation confirmed %+v %v", ds, err)
	}
	if _, err := j.BindGeneration(ctx, delivery.Binding{Agent: m.Parts[0].Agent, Session: m.Session}, true); err != nil {
		t.Fatal(err)
	}
	confirmed, err := j.Deliveries(ctx, delivery.StateConfirmed)
	if err != nil || len(confirmed) != 1 {
		t.Fatalf("rotation erased evidence %+v %v", confirmed, err)
	}
	if ds, err = j.ConfirmHandoff(ctx, m.ID, m.Session, "older", keys, m.CreatedAt); err != nil || len(ds) != 0 {
		t.Fatalf("stale boot %+v %v", ds, err)
	}
	if ds, err = j.ConfirmHandoff(ctx, "hnd_unknown", m.Session, m.Boot, keys, m.CreatedAt); err != nil || len(ds) != 0 {
		t.Fatalf("unknown id %+v %v", ds, err)
	}
}

func TestHandoffReusesOnlyItsExactPendingDelivery(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	id, err := j.AddDelivery(ctx, delivery.Delivery{Agent: m.Parts[0].Agent, Session: m.Session, Boot: m.Boot, State: delivery.StateRetry, Seqs: m.Parts[0].Seqs, CreatedAt: m.CreatedAt, UpdatedAt: m.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	m.Parts[0].DeliveryID = id
	bad := m
	bad.Parts = append([]delivery.HandoffPart(nil), m.Parts...)
	bad.Parts[0].Agent.MemberID = "mem_replacement"
	if _, err := j.PrepareHandoff(ctx, bad); err == nil {
		t.Fatal("replacement reused old row")
	}
	saved, err := j.PrepareHandoff(ctx, m)
	if err != nil || saved.Parts[0].DeliveryID != id {
		t.Fatalf("reuse %+v %v", saved, err)
	}
}

func TestBindingGenerationSurvivesRepeatRenameAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	j := open(t, path)
	b := delivery.Binding{Agent: delivery.AgentRef{Server: "https://team.example", Board: "one", Name: "writer", MemberID: "mem_one"}, Session: delivery.SessionKey{Harness: "codex", ID: "session"}}
	first, err := j.BindGeneration(ctx, b, false)
	if err != nil || first.Generation == 0 {
		t.Fatalf("initial generation %+v %v", first, err)
	}
	b.Agent.Name = "renamed"
	same, err := j.BindGeneration(ctx, b, false)
	if err != nil || same.Generation != first.Generation {
		t.Fatalf("rename advanced %+v %v", same, err)
	}
	rotated, err := j.BindGeneration(ctx, b, true)
	if err != nil || rotated.Generation <= first.Generation {
		t.Fatalf("rotation reused %+v %v", rotated, err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = open(t, path)
	again, err := j.BindGeneration(ctx, b, false)
	if err != nil || again.Generation != rotated.Generation {
		t.Fatalf("restart advanced %+v %v", again, err)
	}
	b.Session.ID = "moved"
	moved, err := j.BindGeneration(ctx, b, false)
	if err != nil || moved.Generation <= rotated.Generation {
		t.Fatalf("session move reused %+v %v", moved, err)
	}
	b.Agent.MemberID = ""
	legacy, err := j.BindGeneration(ctx, b, false)
	if err != nil || legacy.Generation != 0 {
		t.Fatalf("unverified legacy generation %+v %v", legacy, err)
	}
	if _, err := j.PrepareHandoff(ctx, delivery.HandoffManifest{}); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("invalid manifest %v", err)
	}
}

func TestSupersededHandoffCannotConfirmReusedRows(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	old, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range old.Parts {
		if err := j.UpdateDelivery(ctx, delivery.Delivery{ID: p.DeliveryID, Agent: p.Agent, Session: m.Session, Boot: m.Boot, State: delivery.StateRetry, UpdatedAt: m.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	next := old
	next.ID = "hnd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	next.PayloadHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	next, err = j.PrepareHandoff(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	keys := []delivery.AgentKey{m.Parts[0].Agent.Key(), m.Parts[1].Agent.Key()}
	if ds, err := j.ConfirmHandoff(ctx, old.ID, m.Session, m.Boot, keys, m.CreatedAt); err != nil || len(ds) != 0 {
		t.Fatalf("superseded receipt confirmed %+v %v", ds, err)
	}
	if ds, err := j.ConfirmHandoff(ctx, next.ID, m.Session, m.Boot, keys, m.CreatedAt); err != nil || len(ds) != 2 {
		t.Fatalf("current receipt missing %+v %v", ds, err)
	}
}

func TestUpgradeAssignsGenerationOnlyToVerifiedSeats(t *testing.T) {
	ctx := context.Background()
	path := historicalJournal(t, 7)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/008_member_identity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 8"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bindings (server,board,agent,member_id,harness,session_id,bound_at) VALUES ('https://team.example','one','writer','mem_one','codex','one',''),('https://team.example','two','writer','','codex','two','')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	j := open(t, path)
	bs, err := j.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if (b.Agent.MemberID == "") != (b.Generation == 0) {
			t.Fatalf("unverified migration generation %+v", b)
		}
	}
	legacy := delivery.AgentRef{Server: "https://team.example", Board: "two", Name: "writer"}
	verified := legacy
	verified.MemberID = "mem_two"
	if err := j.ResolveIdentity(ctx, legacy, verified); err != nil {
		t.Fatal(err)
	}
	bs, err = j.Bindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		if b.Generation == 0 {
			t.Fatalf("own-token verification did not assign generation %+v", b)
		}
	}
	var count int
	if err := j.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'bindings_one_per_board'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration lost board binding uniqueness %d %v", count, err)
	}
}

func TestHandoffRetryFencesEndedAllocationAndKeepsAttemptHistory(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	m, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Parts {
		if err := j.UpdateDelivery(ctx, delivery.Delivery{ID: p.DeliveryID, Agent: p.Agent, Session: m.Session, Boot: m.Boot, State: delivery.StateRetry, Attempts: 3, CreatedAt: m.CreatedAt, UpdatedAt: m.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := j.PrepareHandoff(ctx, m); err != nil {
		t.Fatal(err)
	}
	keys := []delivery.AgentKey{m.Parts[0].Agent.Key()}
	ds, err := j.ConfirmHandoff(ctx, m.ID, m.Session, m.Boot, keys, m.CreatedAt)
	if err != nil || len(ds) != 1 || ds[0].Attempts != 3 {
		t.Fatalf("retry reset attempt history or confirmed terminal seat %+v %v", ds, err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("received allocation was handed again")
	}
	j, m = handoffFixture(t)
	m, err = j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: m.Session, Boot: "new-boot", Open: true, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("old boot retried")
	}
	if ds, err := j.ConfirmHandoff(ctx, m.ID, m.Session, m.Boot, []delivery.AgentKey{m.Parts[0].Agent.Key(), m.Parts[1].Agent.Key()}, m.CreatedAt); err != nil || len(ds) != 0 {
		t.Fatalf("new boot accepted old receipt %+v %v", ds, err)
	}
}

func TestLegacyProofNeverReusesAnEvictedSeatsGeneration(t *testing.T) {
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	a := delivery.AgentRef{Server: "https://team.example", Board: "one", Name: "writer", MemberID: "mem_one"}
	s := delivery.SessionKey{Harness: "codex", ID: "session"}
	first, err := j.BindGeneration(ctx, delivery.Binding{Agent: a, Session: s}, false)
	if err != nil {
		t.Fatal(err)
	}
	replacement := a
	replacement.MemberID = "mem_other"
	if err := j.Bind(ctx, delivery.Binding{Agent: replacement, Session: s}); err != nil {
		t.Fatal(err)
	}
	legacy := a
	legacy.MemberID = ""
	if err := j.Bind(ctx, delivery.Binding{Agent: legacy, Session: s}); err != nil {
		t.Fatal(err)
	}
	if err := j.ResolveIdentity(ctx, legacy, a); err != nil {
		t.Fatal(err)
	}
	bs, err := j.Bindings(ctx)
	if err != nil || len(bs) != 1 || bs[0].Generation <= first.Generation {
		t.Fatalf("legacy proof reused old generation %+v %v", bs, err)
	}
	before := bs[0].Generation
	if err := j.ResolveIdentity(ctx, legacy, a); err != nil {
		t.Fatal(err)
	}
	bs, err = j.Bindings(ctx)
	if err != nil || bs[0].Generation != before {
		t.Fatalf("idempotent proof advanced generation %+v %v", bs, err)
	}
}

func TestUnconfirmedHandoffRetargetsOnlyToVerifiedNewBinding(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	old, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	p := old.Parts[0]
	if err := j.UpdateDelivery(ctx, delivery.Delivery{ID: p.DeliveryID, Agent: p.Agent, Session: m.Session, Boot: m.Boot, State: delivery.StateRetry, Attempts: 2, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	moved := delivery.SessionKey{Harness: "codex", ID: "moved"}
	b, err := j.BindGeneration(ctx, delivery.Binding{Agent: p.Agent, Session: moved}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: moved, Boot: "new", Open: true, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	next := old
	next.ID = "hnd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	next.Session = moved
	next.Boot = "new"
	next.Parts = []delivery.HandoffPart{p}
	next.Parts[0].Generation = b.Generation
	next, err = j.PrepareHandoff(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if ds, err := j.ConfirmHandoff(ctx, old.ID, old.Session, old.Boot, []delivery.AgentKey{p.Agent.Key()}, m.CreatedAt); err != nil || len(ds) != 0 {
		t.Fatalf("old binding confirmed retargeted row %+v %v", ds, err)
	}
	ds, err := j.ConfirmHandoff(ctx, next.ID, next.Session, next.Boot, []delivery.AgentKey{p.Agent.Key()}, m.CreatedAt)
	if err != nil || len(ds) != 1 || ds[0].ID != p.DeliveryID || ds[0].Session != moved || ds[0].Attempts != 2 {
		t.Fatalf("retarget %+v %v", ds, err)
	}
}

func TestSameSeatHandoffPartsRemainDisjointAndAtomic(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	part := m.Parts[0]
	m.Parts = []delivery.HandoffPart{part, part}
	m.Parts[0].Seqs = []int{1}
	m.Parts[1].Seqs = []int{2}
	id, err := j.AddDelivery(ctx, delivery.Delivery{Agent: part.Agent, Session: m.Session, Boot: m.Boot, State: delivery.StateRetry, Seqs: []int{1}, CreatedAt: m.CreatedAt, UpdatedAt: m.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	m.Parts[0].DeliveryID = id
	bad := m
	bad.Parts = append([]delivery.HandoffPart(nil), m.Parts...)
	bad.Parts[1].Seqs = []int{1}
	if _, err := j.PrepareHandoff(ctx, bad); err == nil {
		t.Fatal("overlapping parts admitted")
	}
	if _, err := j.db.ExecContext(ctx, `CREATE TRIGGER fail_same_seat BEFORE INSERT ON delivery_messages WHEN NEW.seq = 2 BEGIN SELECT RAISE(ABORT,'second row failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("partial same-seat manifest succeeded")
	}
	ds, err := j.Deliveries(ctx, delivery.StateRetry)
	if err != nil || len(ds) != 1 || ds[0].HandoffID != "" {
		t.Fatalf("failed preparation consumed existing row %+v %v", ds, err)
	}
	if _, err := j.db.ExecContext(ctx, "DROP TRIGGER fail_same_seat"); err != nil {
		t.Fatal(err)
	}
	saved, err := j.PrepareHandoff(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	ds, err = j.ConfirmHandoff(ctx, saved.ID, m.Session, m.Boot, []delivery.AgentKey{part.Agent.Key()}, m.CreatedAt)
	if err != nil || len(ds) != 2 {
		t.Fatalf("same-seat rows not confirmed atomically %+v %v", ds, err)
	}
}
