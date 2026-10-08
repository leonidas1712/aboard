//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSayAttachesExistingBoardFileVersions(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-existing-files")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	local := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(local, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := s.run("file", "put", local, "--name", "notes/api.md", "--json").json(t)
	id := field(t, first, "file.id").(string)
	s.run("file", "get", "notes/api.md", local, "--json")
	if err := os.WriteFile(local, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.run("file", "put", local, "--json")
	posted := s.run("say", "Versions to read", "--file", "notes/api.md", "--file", id+"@v1", "--json").json(t)
	matchesCLISpec(t, "SayOutput", posted)
	if field(t, posted, "message.files.0.id") != id || field(t, posted, "message.files.0.version") != float64(2) || field(t, posted, "message.files.1.version") != float64(1) {
		t.Fatalf("wrong pinned versions: %v", posted)
	}
	for _, selector := range []string{"missing.md", "notes/api.md@v99", "notes/api.md@v0", "notes/api.md@vno"} {
		out := s.runExit("say", "Must not post", "--file", selector, "--json")
		code := "file_not_found"
		if selector == "notes/api.md@v0" || selector == "notes/api.md@vno" {
			code = "invalid_request"
		}
		if out.code != 1 || field(t, out.json(t), "error.code") != code {
			t.Fatalf("%s: %s", selector, out.stdout)
		}
		if code == "file_not_found" && !strings.Contains(field(t, out.json(t), "error.hint").(string), "file list") {
			t.Fatal("missing recovery hint")
		}
	}
	textRefusal := s.runExit("say", "No such file", "--file", "missing.md")
	if textRefusal.code != 1 || !strings.Contains(textRefusal.stderr, "file_not_found") || !strings.Contains(textRefusal.stderr, "file list") {
		t.Fatalf("text refusal: %v", textRefusal)
	}
	otherBoard := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, otherBoard)
	other := tm.admin.claudeSession("s-foreign-files")
	other.run("join", "--board", otherBoard, "--server", tm.url(), "--json")
	foreign := other.runExit("say", "Must not cross boards", "--file", id, "--json")
	if foreign.code != 1 || field(t, foreign.json(t), "error.code") != "file_not_found" {
		t.Fatal("foreign file was not refused")
	}
	otherFiles := other.run("file", "list", "--json").json(t)
	if len(field(t, otherFiles, "files").([]any)) != 0 {
		t.Fatal("foreign board acquired an attachment")
	}
	// A missing existing file must refuse before uploading a local attachment.
	extra := filepath.Join(t.TempDir(), "extra.md")
	if err := os.WriteFile(extra, []byte("extra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.runExit("say", "Must not upload", "--attach", extra, "--file", "missing.md", "--json")
	listed := s.run("file", "list", "--json").json(t)
	if len(field(t, listed, "files").([]any)) != 1 {
		t.Fatal("refusal uploaded a file")
	}
	mixed := s.run("say", "Mixed attachments", "--file", "notes/api.md@v1", "--attach", extra, "--json").json(t)
	if len(field(t, mixed, "message.files").([]any)) != 2 {
		t.Fatal("mixed attachments lost a file")
	}
	tm.link(tm.admin, board)
	asked := s.run("ask", "Which version?", "Original", "Latest", "--json").json(t)
	answered := tm.admin.run("say", "--board", board, "--reply", fmt.Sprint(field(t, asked, "message.seq")), "--option", "1", "--file", id+"@v1", "--json").json(t)
	if field(t, answered, "message.files.0.id") != id || field(t, answered, "message.files.0.version") != float64(1) {
		t.Fatal("human answer ignored attachment")
	}
	detail := s.run("file", "show", id, "--json").json(t)
	if len(field(t, detail, "file.posted_in").([]any)) != 4 {
		t.Fatalf("refusal posted a message: %v", detail)
	}
}
