//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func briefWriteLocal(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func briefRefused(t *testing.T, r result, code string) {
	t.Helper()
	if r.code != 1 || errorCode(t, r.json(t)) != code {
		t.Fatalf("want %s refusal: %v", code, r)
	}
}

func TestBriefCLIUpdatesOnlyTheIdentityAndVersionFetched(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	s := tm.admin.claudeSession("s-brief-edit")
	s.run("join", "--board", board, "--server", tm.url(), "--json")
	source := filepath.Join(t.TempDir(), "initial.markdown")
	briefWriteLocal(t, source, "First brief\n")
	created := s.run("brief", "put", source, "--json").json(t)
	matchesCLISpec(t, "BriefPutOutput", created)
	if field(t, created, "name") != "brief.md" || field(t, created, "version") != float64(1) {
		t.Fatalf("create did not normalize the Markdown extension: %v", created)
	}
	read := tm.admin.run("brief", "--json").json(t)
	matchesCLISpec(t, "BriefOutput", read)
	if field(t, read, "text") != "First brief\n" || field(t, read, "board") != board {
		t.Fatalf("person read the wrong brief: %v", read)
	}
	oldID := field(t, read, "brief.file_id")
	personPath := filepath.Join(t.TempDir(), "person.md")
	agentPath := filepath.Join(t.TempDir(), "agent.md")
	matchesCLISpec(t, "FileGetOutput", tm.admin.run("brief", "get", personPath, "--json").json(t))
	s.run("brief", "get", agentPath, "--json")
	briefWriteLocal(t, personPath, "Person's edit\n")
	updated := tm.admin.run("brief", "put", personPath, "--json").json(t)
	if field(t, updated, "version") != float64(2) {
		t.Fatalf("person edit did not replace v1: %v", updated)
	}
	briefWriteLocal(t, agentPath, "Stale agent edit\n")
	briefRefused(t, s.runExit("brief", "put", agentPath, "--json"), "file_changed")
	data, err := os.ReadFile(agentPath)
	if err != nil || string(data) != "Stale agent edit\n" {
		t.Fatalf("refusal changed local edits: %q, %v", data, err)
	}
	blind := filepath.Join(t.TempDir(), "blind.md")
	briefWriteLocal(t, blind, "Blind edit\n")
	briefRefused(t, s.runExit("brief", "put", blind, "--base", "2", "--json"), "file_changed")
	current := s.run("brief", "--json").json(t)
	if field(t, current, "brief.file_id") != oldID || field(t, current, "brief.version") != float64(2) || field(t, current, "text") != "Person's edit\n" {
		t.Fatalf("refused writes changed the brief: %v", current)
	}
	s.run("audit", "verify", "--json")
}

func TestBriefCLIFormatSwitchKeepsTheOldFileHistory(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	source := filepath.Join(t.TempDir(), "source.md")
	briefWriteLocal(t, source, "Original Markdown\n")
	tm.admin.run("brief", "put", source, "--json")
	old := tm.admin.run("brief", "--json").json(t)
	oldID := field(t, old, "brief.file_id").(string)
	html := filepath.Join(t.TempDir(), "replacement.html")
	tm.admin.run("brief", "get", html, "--json")
	briefWriteLocal(t, html, "<h1>HTML brief</h1>\n")
	briefRefused(t, tm.admin.runExit("brief", "put", html, "--json"), "brief_exists")
	switched := tm.admin.run("brief", "put", html, "--replace-format", "--json").json(t)
	matchesCLISpec(t, "BriefPutOutput", switched)
	if field(t, switched, "name") != "brief.html" || field(t, switched, "version") != float64(1) || field(t, switched, "replaced") != "brief.md" {
		t.Fatalf("format switch did not replace the old format: %v", switched)
	}
	current := tm.admin.run("brief", "--json").json(t)
	if field(t, current, "brief.file_id") == oldID || field(t, current, "text") != "<h1>HTML brief</h1>\n" {
		t.Fatalf("format switch did not create a new exact file: %v", current)
	}
	history := filepath.Join(t.TempDir(), "history.md")
	tm.admin.run("file", "get", oldID, history, "--version", "1", "--json")
	data, err := os.ReadFile(history)
	if err != nil || string(data) != "Original Markdown\n" {
		t.Fatalf("old file bytes did not survive replacement: %q, %v", data, err)
	}
	listed := tm.admin.run("file", "list", "--json").json(t)
	if len(field(t, listed, "files").([]any)) != 1 || field(t, listed, "files.0.id") != field(t, current, "brief.file_id") {
		t.Fatalf("both formats remained active: %v", listed)
	}
}

func TestBriefCLIStdoutDoesNotRememberAnEditBase(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	source := filepath.Join(t.TempDir(), "source.md")
	briefWriteLocal(t, source, "Brief bytes\n")
	tm.admin.run("brief", "put", source, "--json")
	if got := tm.admin.run("brief", "get", "-").stdout; got != "Brief bytes\n" {
		t.Fatalf("stdout download changed bytes: %q", got)
	}
	blind := filepath.Join(t.TempDir(), "copied.md")
	briefWriteLocal(t, blind, "Edited stdout copy\n")
	briefRefused(t, tm.admin.runExit("brief", "put", blind, "--base", "1", "--json"), "file_changed")
	current := tm.admin.run("brief", "--json").json(t)
	if field(t, current, "brief.version") != float64(1) || field(t, current, "text") != "Brief bytes\n" {
		t.Fatalf("stdout copy overwrote the brief: %v", current)
	}
}

func TestBriefCLISelectsTheExactBoardAndSeat(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	first, second := tm.newBoard(tm.admin, "open"), tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, first)
	s := tm.admin.claudeSession("s-brief-boards")
	seat := s.run("join", "--board", first, "--server", tm.url(), "--json").json(t)
	s.run("join", "--board", second, "--server", tm.url(), "--json")
	source := filepath.Join(t.TempDir(), "source.md")
	briefWriteLocal(t, source, "First board only\n")
	briefRefused(t, s.runExit("brief", "put", source, "--json"), "board_ambiguous")
	put := s.run("brief", "put", source, "--board", first, "--json").json(t)
	if field(t, put, "board") != first {
		t.Fatalf("explicit board was not preserved: %v", put)
	}
	status, untouched := tm.call("GET", "/v1/boards/"+second, tm.key(tm.admin), nil)
	if status != http.StatusOK || untouched["brief"] != nil {
		t.Fatalf("write affected the sibling board: %d %v", status, untouched)
	}
	read := s.run("brief", "--board", first, "--json").json(t)
	if field(t, read, "brief.by.name") != field(t, seat, "agent.name") || field(t, read, "brief.by.kind") != "agent" {
		t.Fatalf("agent brief used another actor: %v", read)
	}
	briefRefused(t, s.runExit("brief", "--board", first, "--as", "missing-agent", "--json"), "agent_not_selected")
	if field(t, tm.admin.run("brief", "--json").json(t), "text") != "First board only\n" {
		t.Fatal("person's folder board did not remain readable")
	}
}
