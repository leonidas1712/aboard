package sqlitejournal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

func TestNoticeBookkeepingRetainsPermanentSeatAndOriginalOrigin(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "journal.db")
	j, err := sqlitejournal.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	a := delivery.AgentRef{Server: "https://team.example", MemberID: "mem_own", Board: "before", Name: "writer"}
	n := delivery.DurableNotice{ID: "notice_one", Kind: "approval_outcome", SourceID: "apr_own", Agent: a, Session: delivery.SessionKey{Harness: "codex", ID: "origin"}, Boot: "b1", Generation: 1}
	if err := j.SaveNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = sqlitejournal.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	a.Board = "after"
	rows, err := j.Notices(ctx, a)
	if err != nil || len(rows) != 1 || rows[0].Handed || rows[0].Agent.Board != "before" {
		t.Fatal(rows, err)
	}
	foreign := a
	foreign.Server = "https://foreign.example"
	rows, err = j.Notices(ctx, foreign)
	if err != nil || len(rows) != 0 {
		t.Fatal("foreign notice", rows, err)
	}
	changed := n
	changed.Session.ID = "guessed"
	if err := j.SaveNotice(ctx, changed); err == nil {
		t.Fatal("origin transferred")
	}
	if err := j.MarkNoticeHanded(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	rows, err = j.Notices(ctx, a)
	if err != nil || len(rows) != 1 || !rows[0].Handed {
		t.Fatal(rows, err)
	}
	manifests, err := j.Handoffs(ctx)
	if err != nil || len(manifests) != 0 {
		t.Fatal("bookkeeping fabricated proof", manifests, err)
	}
}
