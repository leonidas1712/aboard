package sqlitejournal

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"path/filepath"
	"testing"
	"time"
)

func TestApprovalWatchRetainsOriginalSessionAndRejectsReplacement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	j := open(t, path)
	key := delivery.SessionKey{Harness: "claude-code", ID: "original"}
	agent := delivery.AgentRef{Server: "https://team.example", Board: "work", MemberID: "mem_own"}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: key, Boot: "boot", Open: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	b, err := j.BindGeneration(ctx, delivery.Binding{Agent: agent, Session: key, BoundAt: time.Now()}, false)
	if err != nil {
		t.Fatal(err)
	}
	w := delivery.ApprovalWatch{ID: "apr_own", Agent: agent, Session: key, Boot: "boot", Generation: b.Generation}
	if err := j.SaveApprovalWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = open(t, path)
	rows, err := j.ApprovalWatches(ctx, key)
	if err != nil || len(rows) != 1 || rows[0].Generation != b.Generation {
		t.Fatalf("restart lost originating fence: %+v %v", rows, err)
	}
	other := delivery.SessionKey{Harness: "claude-code", ID: "replacement"}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: other, Boot: "other_boot", Open: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	replacement, err := j.BindGeneration(ctx, delivery.Binding{Agent: agent, Session: other, BoundAt: time.Now()}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SaveApprovalWatch(ctx, w); err == nil {
		t.Fatal("old fence wrote after rebind")
	}
	w.Session, w.Boot, w.Generation = other, "other_boot", replacement.Generation
	if err := j.SaveApprovalWatch(ctx, w); err == nil {
		t.Fatal("replacement inherited approval origin")
	}
	rows, err = j.ApprovalWatches(ctx, other)
	if err != nil || len(rows) != 0 {
		t.Fatalf("foreign origin saved: %+v %v", rows, err)
	}
}
