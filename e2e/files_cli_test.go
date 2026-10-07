//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCLIUpdatesTheVersionFetchedToALocalPath(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-file-cli")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	local := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(local, []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	put := s.run("file", "put", local, "--name", "notes/api.md", "--json").json(t)
	matchesCLISpec(t, "FileOutput", put)
	if field(t, put, "file.latest.version") != float64(1) {
		t.Fatalf("first version: %v", put)
	}
	listed := s.run("file", "list", "--json").json(t)
	matchesCLISpec(t, "FileListOutput", listed)
	shown := s.run("file", "show", "notes/api.md", "--json").json(t)
	matchesCLISpec(t, "FileOutput", shown)
	dest := filepath.Join(t.TempDir(), "editable.md")
	got := s.run("file", "get", "notes/api.md", dest, "--json").json(t)
	matchesCLISpec(t, "FileGetOutput", got)
	if err := os.WriteFile(dest, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	put = s.run("file", "put", dest, "--json").json(t)
	if field(t, put, "file.name") != "notes/api.md" || field(t, put, "file.latest.version") != float64(2) {
		t.Fatalf("remembered file: %v", put)
	}
	s.run("file", "get", "notes/api.md", dest, "--json")
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "second\n" {
		t.Fatalf("download: %q, %v", data, err)
	}
	moved := s.run("file", "mv", "notes/api.md", "notes/renamed.md", "--json").json(t)
	matchesCLISpec(t, "FileOutput", moved)
	s.run("file", "rm", "notes/renamed.md", "--json")
	listed = s.run("file", "list", "--json").json(t)
	if len(field(t, listed, "files").([]any)) != 0 {
		t.Fatal("removed file still listed")
	}
	checked := tm.admin.run("storage", "check", "--json").json(t)
	matchesCLISpec(t, "StorageCheckOutput", checked)
	if field(t, checked, "checked") != float64(2) {
		t.Fatal("storage check omitted historical versions")
	}
	destination := "disk://" + filepath.Join(t.TempDir(), "copied")
	copied := tm.admin.run("storage", "copy", "--from", "disk://"+filepath.Join(tm.admin.dataDir(), "files"), "--to", destination, "--json").json(t)
	matchesCLISpec(t, "StorageCopyOutput", copied)
	if field(t, copied, "copied") != float64(2) {
		t.Fatal("copy omitted historical bytes")
	}
	attachment := filepath.Join(t.TempDir(), "attached.md")
	if err := os.WriteFile(attachment, []byte("attachment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	message := s.run("say", "A file to read", "--attach", attachment, "--json").json(t)
	if field(t, message, "message.files.0.version") != float64(1) {
		t.Fatal("message did not pin uploaded version")
	}
	refused := s.runExit("storage", "check", "--json")
	if refused.code != 1 || field(t, refused.json(t), "error.code") != "human_command_in_session" {
		t.Fatal("agent could run storage maintenance")
	}
	s.run("audit", "verify", "--json")
}
