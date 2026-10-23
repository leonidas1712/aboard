package board_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/blobs/disk"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
)

// Count rows returned by the real store, so the regression does not depend on
// machine speed and cannot pass by merely moving the repeated scans elsewhere.
type fileHistoryReads struct {
	board.Store
	messages int
}
type fileHistoryTx struct {
	board.ReadTx
	reads *fileHistoryReads
}

func (s *fileHistoryReads) Read(ctx context.Context, fn func(board.ReadTx) error) error {
	return s.Store.Read(ctx, func(tx board.ReadTx) error { return fn(fileHistoryTx{tx, s}) })
}

func (tx fileHistoryTx) Timeline(id string, me board.Member, all bool, q board.TimelineQuery) ([]board.Message, error) {
	ms, err := tx.ReadTx.Timeline(id, me, all, q)
	tx.reads.messages += len(ms)
	return ms, err
}

func TestFileListingReadsMessageHistoryOnce(t *testing.T) {
	w := newKeyWorld(t)
	blobs, err := disk.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	reads := &fileHistoryReads{Store: w.gate.Store}
	cfg := w.svc.Config()
	cfg.Blobs = blobs
	w.svc = board.New(reads, notify.NewInProcess(), w.clk, ids.New(rand.Reader), digestKey, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p := w.auth(t, w.owner)
	ctx := t.Context()
	for i := 0; i < 20; i++ {
		_, err := w.svc.PutFile(ctx, p, w.board, board.NewFile{Name: fmt.Sprintf("file-%d.md", i), MediaType: "text/plain", Body: strings.NewReader("draft")})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 205; i++ {
		_, err := w.svc.PostMessage(ctx, p, w.board, board.NewMessage{Body: "update", Files: []board.FileSelector{{File: "file-0.md"}}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 100 {
			_, err = w.svc.PutFile(ctx, p, w.board, board.NewFile{Name: "file-1.md", Base: 1, MediaType: "text/plain", Body: strings.NewReader("revised")})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	reads.messages = 0
	files, more, err := w.svc.ListFiles(ctx, p, w.board, board.FileFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(files) != 20 {
		t.Fatalf("listing: %d files, more %v", len(files), more)
	}
	if reads.messages != 205 {
		t.Errorf("listing read %d message rows, want one pass of 205", reads.messages)
	}
	for _, f := range files {
		want := 205
		if f.Name == "file-1.md" {
			want = 104
		}
		if f.MessagesSince != want || f.AnswersSince != 0 || f.TasksDoneSince != 0 {
			t.Errorf("%s freshness: messages %d, answers %d, tasks %d", f.Name, f.MessagesSince, f.AnswersSince, f.TasksDoneSince)
		}
		posts := 0
		if f.Name == "file-0.md" {
			posts = 205
		}
		if len(f.PostedIn) != posts {
			t.Errorf("%s posted in %d, want %d", f.Name, len(f.PostedIn), posts)
		}
		for _, v := range f.Versions {
			if v.By.ID == "" {
				t.Error("missing author")
			}
		}
	}
}
