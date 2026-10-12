//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportSavesVisibleContentWithoutReplacingALocalFile(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	b := tm.newBoard(tm.admin, "open")
	s := tm.admin.claudeSession("s-export")
	s.run("join", "--board", b, "--server", tm.url(), "--json")
	s.run("say", "Export this message", "--json")
	dest := filepath.Join(t.TempDir(), "saved.md")
	out := s.run("export", "--out", dest, "--json").json(t)
	matchesCLISpec(t, "ExportOutput", out)
	if field(t, out, "path") != dest || field(t, out, "format") != "md" {
		t.Fatalf("output: %v", out)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Export this message") {
		t.Fatal("saved export omitted message")
	}
	defaultOut := s.run("export", "--json").json(t)
	defaultPath := field(t, defaultOut, "path").(string)
	if !strings.HasPrefix(filepath.Base(defaultPath), b+"-") || !strings.HasSuffix(defaultPath, ".md") {
		t.Fatalf("default path: %s", defaultPath)
	}
	zipPath := filepath.Join(t.TempDir(), "board.zip")
	zipped := s.run("export", "--zip", "--out", zipPath, "--json").json(t)
	matchesCLISpec(t, "ExportOutput", zipped)
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil || field(t, zipped, "format") != "zip" || !strings.HasPrefix(string(zipBytes), "PK") {
		t.Fatalf("zip export: %v, %v", zipped, err)
	}
	again := s.runExit("export", "--out", dest, "--json")
	if again.code != 1 || errorCode(t, again.json(t)) != "output_exists" {
		t.Fatalf("overwrite: %v", again)
	}
	after, err := os.ReadFile(dest)
	if err != nil || string(after) != string(data) {
		t.Fatal("existing export was replaced")
	}
}
