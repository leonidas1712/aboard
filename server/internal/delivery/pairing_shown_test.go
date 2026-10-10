package delivery_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

func TestPairingShownReceiptsRequireCurrentIssuerBoardSessionBootAndGeneration(t *testing.T) {
	ctx := context.Background()
	j, err := sqlitejournal.Open(ctx, filepath.Join(t.TempDir(), "journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	key := delivery.SessionKey{Harness: "codex", ID: "selected"}
	agent := delivery.AgentRef{Server: "https://a.example", MemberID: "mem_same", Board: "work", Name: "writer"}
	now := time.Now()
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: key, Boot: "boot", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	binding, err := j.BindGeneration(ctx, delivery.Binding{Session: key, Agent: agent, BoundAt: now}, false)
	if err != nil {
		t.Fatal(err)
	}
	row := delivery.ShownRecord{Session: key, Boot: "boot", Agent: agent, Generation: binding.Generation, Message: delivery.ShownMessage{BoardID: "brd_same", MemberID: agent.MemberID, MessageID: "msg_exact", Seq: 8}}
	if err := j.SaveShown(ctx, []delivery.ShownRecord{row, row}); err != nil {
		t.Fatal(err)
	}
	check := func(k delivery.SessionKey, a delivery.AgentRef, b string, want int) {
		t.Helper()
		rows, err := delivery.PairingShownRows(ctx, j, k, a, b)
		if err != nil || len(rows) != want {
			t.Fatalf("shown proof=%+v err=%v want=%d", rows, err, want)
		}
	}
	check(key, agent, "brd_same", 1)
	foreign := agent
	foreign.Server = "https://b.example"
	check(key, foreign, "brd_same", 0)
	check(key, agent, "brd_other", 0)
	check(delivery.SessionKey{Harness: "codex", ID: "another"}, agent, "brd_same", 0)
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: key, Boot: "newboot", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	check(key, agent, "brd_same", 0)
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: key, Boot: "boot", Open: false, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	check(key, agent, "brd_same", 0)
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: key, Boot: "boot", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.BindGeneration(ctx, delivery.Binding{Session: key, Agent: agent, BoundAt: now}, true); err != nil {
		t.Fatal(err)
	}
	check(key, agent, "brd_same", 0)
}
