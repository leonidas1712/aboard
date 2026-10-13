//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBareJoinCodeUsesTheInviteDefaultInsteadOfTheFolder(t *testing.T) {
	t.Parallel()
	tm, other := newTeam(t), newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	person := tm.person("sam")
	tm.admin.run("board", "add", "@sam", "--board", board)
	person.run("up")
	person.run("servers", "name", tm.url(), "qa")
	person.run("servers", "use", "qa")
	legacy, err := json.Marshal(map[string]any{"server": map[string]string{"url": other.url()}, "board": "unused"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(person.dir, ".aboard"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	invite := person.run("invite", "--board", board, "--json").json(t)
	words := strings.Fields(field(t, invite, "join_line").(string))
	if len(words) == 0 {
		t.Fatal("invite omitted its join line")
	}
	code := words[len(words)-1]
	joined := person.run("join", code, "--name", "from-default", "--harness", "claude-code", "--json").json(t)
	if field(t, joined, "server.url") != tm.url() || field(t, joined, "board.name") != board {
		t.Fatalf("bare code did not follow invite's issuer: %v", joined)
	}
	person.run("servers", "use", "local")
	wrong := person.runExit("join", code, "--server", "local", "--json")
	if wrong.code != 1 || errorCode(t, wrong.json(t)) != "join_code_invalid" {
		t.Fatalf("explicit issuer must not retry another server: %s", wrong)
	}
	explicit := person.run("join", code, "--server", "qa", "--name", "from-flag", "--harness", "claude-code", "--json").json(t)
	if field(t, explicit, "server.url") != tm.url() {
		t.Fatalf("explicit issuer ignored: %v", explicit)
	}
	line := field(t, invite, "join_line").(string)
	conflict := person.runExit("join", line, "--server", "local", "--json")
	if conflict.code != 2 || errorCode(t, conflict.json(t)) != "invalid_request" {
		t.Fatalf("a flag must not override the join line's issuer: %s", conflict)
	}
	person.run("join", line, "--name", "from-line", "--harness", "claude-code")
}
