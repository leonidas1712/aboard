//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownPersonBoardNamesOnlyReadableChoices(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := tm.person("maya")
	hidden := tm.newBoard(tm.admin, "private")
	for i := range 12 {
		person.run("board", "new", fmt.Sprintf("choice-%02d", i), "--private")
	}
	for _, name := range []string{"missing-board", hidden} {
		for _, command := range [][]string{{"status"}, {"board", "people"}} {
			got := person.runExit(append(command, "--board", name, "--json")...)
			out := got.json(t)
			if got.code != 1 || errorCode(t, out) != "board_not_found" {
				t.Fatalf("unknown board: %s", got)
			}
			choices := field(t, out, "error.details.boards").([]any)
			if len(choices) != 10 {
				t.Fatalf("choices not bounded: %v", choices)
			}
			for i, choice := range choices {
				if choice != fmt.Sprintf("choice-%02d", i) {
					t.Fatalf("wrong readable choices: %v", choices)
				}
			}
			hint := field(t, out, "error.hint").(string)
			if strings.Contains(hint, hidden) || !strings.Contains(hint, "choice-00") || strings.Contains(hint, "choice-11") {
				t.Fatalf("wrong hint: %q", hint)
			}
		}
	}
}

func TestRecoveryWordingMatchesDefaultServer(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	other := newTeam(t)
	tm.admin.run("connect", other.invite(), "--handle", "local-owner")
	used := tm.admin.run("servers", "use", "local")
	if !strings.Contains(used.stdout, "Person commands now use "+tm.url()+" by default.") || strings.Contains(used.stdout, "linked folder") {
		t.Fatalf("server-use wording: %s", used)
	}
	link := filepath.Join(tm.admin.dir, ".aboard")
	if err := os.WriteFile(link, []byte(`ignored legacy link`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := tm.admin.runExit("doctor", "--json").json(t)
	found := false
	for _, raw := range field(t, got, "checks").([]any) {
		check := raw.(map[string]any)
		if check["name"] == "legacy_folder_link" {
			found = true
			if check["fix"] != "Delete it: rm -rf "+link {
				t.Fatalf("legacy recovery: %v", check)
			}
		}
	}
	if !found {
		t.Fatal("legacy link warning missing")
	}
	text := tm.admin.runExit("doctor").stdout
	for _, bad := range []string{"For your local server, use --server local", "servers useNAME", "boardagain"} {
		if strings.Contains(text, bad) {
			t.Fatalf("bad recovery wording %q: %s", bad, text)
		}
	}
}
