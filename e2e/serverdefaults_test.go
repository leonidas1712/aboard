//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultServerIgnoresLegacyFolderAndChoosesOnlyBoard(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	person := first.person("sam")
	person.run("connect", second.invite())
	for _, srv := range []string{first.url(), second.url()} {
		person.run("board", "new", "same", "--server", srv)
	}
	legacy, err := json.Marshal(map[string]any{"server": map[string]string{"name": first.url(), "url": first.url()}, "board": "same"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(person.dir, ".aboard")
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	person.run("servers", "use", second.url())
	listed := person.run("boards", "--json").json(t)
	if field(t, listed, "server.url") != second.url() || listed["servers"] != nil {
		t.Fatalf("board list ignored persisted default: %v", listed)
	}
	status := person.run("status", "--json").json(t)
	if field(t, status, "server.url") != second.url() || field(t, status, "board") != "same" {
		t.Fatalf("sole readable board selection: %v", status)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(legacy) {
		t.Fatalf("legacy link was changed: %q, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	person.run("status", "--board", "same", "--json")
}
