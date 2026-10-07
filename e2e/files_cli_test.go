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
	if err := os.WriteFile(local, []byte("first\n"), 0o600); err != nil { t.Fatal(err) }
	put := s.run("file", "put", local, "--name", "notes/api.md", "--json").json(t)
	matchesCLISpec(t, "FileOutput", put)
	if field(t, put, "file.latest.version") != float64(1) { t.Fatalf("first version: %v", put) }
	listed := s.run("file", "list", "--json").json(t)
	matchesCLISpec(t, "FileListOutput", listed)
	dest := filepath.Join(t.TempDir(), "editable.md")
	got := s.run("file", "get", "notes/api.md", dest, "--json").json(t)
	matchesCLISpec(t, "FileGetOutput", got)
	if err := os.WriteFile(dest, []byte("second\n"), 0o600); err != nil { t.Fatal(err) }
	put = s.run("file", "put", dest, "--json").json(t)
	if field(t, put, "file.name") != "notes/api.md" || field(t, put, "file.latest.version") != float64(2) { t.Fatalf("remembered file: %v", put) }
	s.run("file", "get", "notes/api.md", dest, "--json")
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "second\n" { t.Fatalf("download: %q, %v", data, err) }
	s.run("audit", "verify", "--json")
}
