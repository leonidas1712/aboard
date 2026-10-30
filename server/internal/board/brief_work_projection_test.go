package board_test

import (
	"crypto/rand"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/blobs/disk"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

func TestTaskWorkSkipsHistoryForAnotherAuthorsBrief(t *testing.T) {
	w := newTeamWorld(t)
	blobs, err := disk.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	reads := &fileHistoryReads{Store: w.gate.Store}
	cfg := w.svc.Config()
	cfg.Blobs = blobs
	w.svc = board.New(reads, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := t.Context()
	before, err := w.svc.TaskWork(ctx, w.samAgent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.svc.PutFile(ctx, w.maya, w.board, board.NewFile{Brief: true, Name: "brief.md", MediaType: "text/plain", Body: strings.NewReader("current summary")})
	if err != nil {
		t.Fatal(err)
	}
	for range 205 {
		if _, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "update"}); err != nil {
			t.Fatal(err)
		}
	}
	reads.messages = 0
	got, err := w.svc.TaskWork(ctx, w.samAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, got) {
		t.Fatalf("another author's brief changed work: before %+v, after %+v", before, got)
	}
	if reads.messages != 0 {
		t.Fatalf("read %d history rows for a brief omitted from work", reads.messages)
	}
	reads.messages = 0
	inbox, _, err := w.svc.Inbox(ctx, w.samAgent, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if inbox.Work == nil || !reflect.DeepEqual(got, *inbox.Work) || reads.messages != 0 {
		t.Fatalf("inbox projected another author's brief: work %+v, history rows %d", inbox.Work, reads.messages)
	}
	// Taking authorship must still expose fresh counters to this agent.
	_, err = w.svc.PutFile(ctx, w.samAgent, w.board, board.NewFile{Brief: true, Name: "brief.md", Base: 1, FileID: &first.ID, MediaType: "text/plain", Body: strings.NewReader("agent summary")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "after summary"}); err != nil {
		t.Fatal(err)
	}
	got, err = w.svc.TaskWork(ctx, w.samAgent)
	if err != nil {
		t.Fatal(err)
	}
	if got.Brief == nil || got.Brief.MessagesSince != 1 || got.Brief.Versions[len(got.Brief.Versions)-1].ByID != w.samAgent.Agent.ID {
		t.Fatalf("author lost brief freshness: %+v", got.Brief)
	}
}
