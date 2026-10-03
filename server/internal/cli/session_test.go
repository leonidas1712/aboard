package cli

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestAgentByName(t *testing.T) {
	const srv = "http://127.0.0.1:7400"
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "docs", Name: "writer", Token: "t1"},
		{Server: srv, Board: "docs-2", Name: "writer", Token: "t2"},
		{Server: srv, Board: "docs", Name: "reviewer", Token: "t3"},
	}}
	tests := []struct {
		name, as, board, project string
		wantToken, wantCode      string
		wantBoards               []string
	}{
		{"one board", "reviewer", "", "", "t3", "", nil},
		{"two boards need a choice", "writer", "", "", "", "agent_ambiguous", []string{"docs", "docs-2"}},
		{"--board picks", "writer", "docs-2", "", "t2", "", nil},
		{"the directory's board picks", "writer", "", "docs", "t1", "", nil},
		{"unknown name", "ghost", "", "", "", "agent_not_selected", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			a := &app{env: Env{Dir: dir, Getenv: func(k string) string {
				return map[string]string{"HOME": dir, "ABOARD_LOCAL_ADDR": "127.0.0.1:7400"}[k]
			}}}
			if tt.project != "" {
				if err := writeJSONFile(filepath.Join(dir, projectFileName), projectFile{Server: serverRef{Name: "local", URL: srv}, Board: tt.project}, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			tg, cred, err := a.agentByName(creds, tt.as, tt.board)
			if tt.wantCode != "" {
				e := asError(err)
				if e.Code != tt.wantCode {
					t.Fatalf("code %q, want %q (%v)", e.Code, tt.wantCode, err)
				}
				boards, _ := e.Details["boards"].([]string)
				if tt.wantBoards != nil && !slices.Equal(boards, tt.wantBoards) {
					t.Fatalf("boards %v", e.Details["boards"])
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cred.Token != tt.wantToken || tg.board != cred.Board || tg.server.Name != "local" {
				t.Fatalf("got %+v on %+v", cred, tg)
			}
		})
	}
}

// A Codex started inside a Claude Code session inherits its ABOARD_SESSION, and is
// taken for Codex: the variable Codex sets for each command is the more specific one.
func TestSessionKeyPrefersTheMostSpecificMarker(t *testing.T) {
	env := map[string]string{"ABOARD_SESSION": "claude-code:5f1c", "CODEX_THREAD_ID": "019a"}
	a := &app{env: Env{Getenv: func(k string) string { return env[k] }}}
	if k, ok := a.sessionKey(); !ok || k.String() != "codex:019a" {
		t.Fatalf("got %v %v", k, ok)
	}
	delete(env, "CODEX_THREAD_ID")
	if k, ok := a.sessionKey(); !ok || k.String() != "claude-code:5f1c" {
		t.Fatalf("got %v %v", k, ok)
	}
	delete(env, "ABOARD_SESSION")
	if _, ok := a.sessionKey(); ok {
		t.Fatal("found a session in a plain terminal")
	}
}

func TestSessionIDsWrittenToTheEnvironmentFileAreSafe(t *testing.T) {
	for id, ok := range map[string]bool{
		"5f1c2d3e-0000-4000-8000-000000000001": true,
		"s-writer":                             true,
		"x; rm -rf ~":                          false,
		"$(whoami)":                            false,
		"":                                     false,
		"a\nexport X=1":                        false,
	} {
		if sessionIDPattern.MatchString(id) != ok {
			t.Errorf("%q accepted = %v, want %v", id, !ok, ok)
		}
	}
}
