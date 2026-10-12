package export

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestMarkdownAndZipGolden(t *testing.T) {
	title := "Launch notes"
	owner := "alex"
	root := "root"
	one := 1
	done := "Finished."
	person := board.Member{Name: "alex", Kind: "human"}
	writer := board.Member{Name: "writer", Kind: "agent", Owner: &owner}
	d := Document{
		Board: board.Board{Name: "launch", Title: &title}, Server: "https://team.example", At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), By: "@alex", BriefName: "brief.html", Brief: []byte("<h1>Keep this source</h1>"),
		Tasks: []board.Task{
			{Number: 3, Ref: "LA-3", Title: "Open task", State: "open"},
			{Number: 2, Ref: "LA-2", Title: "Writing", State: "in_progress", Owner: &writer, Helpers: []board.Member{person}, Stands: &board.TaskText{Text: "Draft ready.\n```\nReview next.", By: writer}},
			{Number: 1, Ref: "LA-1", Title: "Finished", State: "done", Owner: &person, ClosedNote: &done},
		},
		Messages: []board.Message{
			{ID: root, Seq: 1, SenderName: "writer", SenderOwner: &owner, At: "2026-01-02T00:00:00Z", Body: "Choose a format", Ask: &board.Ask{Options: []string{"Markdown", "HTML"}, State: "answered"}},
			{ID: "answer", Seq: 2, SenderName: "alex", At: "2026-01-02T00:01:00Z", Body: "Use Markdown.", ThreadRoot: &root, Answer: &board.Answer{AskID: root, Option: &one}},
			{ID: "file", Seq: 3, SenderName: "writer", SenderOwner: &owner, At: "2026-01-02T00:02:00Z", Body: "<script>never execute</script>", Files: []board.FileRef{{Name: "notes/report.txt", Version: 1}}, Reactions: []board.ReactionCount{{Emoji: "✅", By: []string{"alex"}}}},
		},
		Files: []board.File{{Name: "notes/report.txt", Versions: []board.FileVersion{{Version: 2, Size: 14, By: writer, At: "2026-01-02T00:03:00Z"}}}},
	}
	want, err := os.ReadFile(filepath.Join("testdata", "board.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Markdown(); got != string(want) {
		t.Fatalf("Markdown differs from golden:\n%s", got)
	}
	var first, second bytes.Buffer
	open := func(board.File) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("latest bytes\n")), nil }
	if err := d.Zip(&first, open); err != nil {
		t.Fatal(err)
	}
	if err := d.Zip(&second, open); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("zip ordering or metadata is nondeterministic")
	}
	z, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		found[f.Name] = string(body)
	}
	if found["board.md"] != string(want) || found["brief.html"] != string(d.Brief) || found["files/notes/report.txt"] != "latest bytes\n" {
		t.Fatal("zip content changed")
	}
}
