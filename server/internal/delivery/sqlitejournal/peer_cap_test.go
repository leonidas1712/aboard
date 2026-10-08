package sqlitejournal

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestPeerCapIsAtomicAndRetriesDoNotReplenishIt(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	m.Class = delivery.ClassMidturnPeer
	m.PeerTurn = 1
	m.PeerSenders = []delivery.AgentKey{{Server: "https://team.example", MemberID: "mem_sender"}}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: m.Session, Boot: m.Boot, Open: true, PeerTurnActive: true, PeerTurn: 1, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err != nil {
		t.Fatalf("immutable retry failed: %v", err)
	}
	var index int
	var database, path string
	if err := j.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&index, &database, &path); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = open(t, path)
	next := m
	next.ID = "hnd_1123456789abcdef0123456789abcdef"
	for i := range next.Parts {
		next.Parts[i].Seqs = []int{3}
	}
	if _, err := j.PrepareHandoff(ctx, next); err == nil {
		t.Fatal("same sender obtained a second allocation in the same turn")
	}
	var count int
	if err := j.db.QueryRowContext(ctx, "SELECT count(*) FROM handoffs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("refused allocation changed handoffs: %d %v", count, err)
	}
	// A new boot is still the same logical turn.
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: m.Session, Boot: "reconnected", Open: true, PeerTurnActive: true, PeerTurn: 1, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	next.Boot = "reconnected"
	if _, err := j.PrepareHandoff(ctx, next); err == nil {
		t.Fatal("reconnect replenished the sender allowance")
	}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: m.Session, Boot: next.Boot, Open: true, PeerTurnActive: true, PeerTurn: 2, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	next.PeerTurn = 2
	if _, err := j.PrepareHandoff(ctx, next); err != nil {
		t.Fatalf("next turn had no allowance: %v", err)
	}
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("old-turn allocation was retried in a newer turn")
	}
	sessions, err := j.Sessions(ctx)
	if err != nil || len(sessions) != 1 || !sessions[0].PeerTurnActive || sessions[0].PeerTurn != 2 {
		t.Fatalf("turn was not durably retained: %+v %v", sessions, err)
	}
}

func TestPeerCapRollsBackWithFailedSeatAllocation(t *testing.T) {
	ctx := context.Background()
	j, m := handoffFixture(t)
	m.Class, m.PeerTurn = delivery.ClassMidturnPeer, 1
	m.PeerSenders = []delivery.AgentKey{{Server: "https://team.example", MemberID: "mem_sender"}}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: m.Session, Boot: m.Boot, Open: true, PeerTurnActive: true, PeerTurn: 1, UpdatedAt: m.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	m.Parts[1].Generation++
	if _, err := j.PrepareHandoff(ctx, m); err == nil {
		t.Fatal("stale seat was allocated")
	}
	m.Parts[1].Generation--
	if _, err := j.PrepareHandoff(ctx, m); err != nil {
		t.Fatalf("failed allocation consumed the allowance: %v", err)
	}
}
