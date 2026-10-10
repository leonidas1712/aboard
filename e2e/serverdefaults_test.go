//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFreshAndStoppedLocalDiagnosticsNeedNoServerSelection(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, state := range []string{"fresh", "stopped"} {
		if state == "stopped" {
			e.run("up")
			e.run("down")
		}
		status := e.run("status", "--json").json(t)
		if status["server_running"] != false || field(t, status, "server.url") != "http://"+e.addr {
			t.Fatalf("%s status: %v", state, status)
		}
		if text := e.run("status").stdout; !strings.Contains(text, "not running; aboard up starts it") {
			t.Fatalf("%s status has no start guidance: %s", state, text)
		}
		doctor := e.runExit("doctor", "--json")
		if doctor.code != 0 && doctor.code != 3 {
			t.Fatalf("%s doctor refused diagnostics: %s", state, doctor)
		}
		found := false
		for _, raw := range field(t, doctor.json(t), "checks").([]any) {
			check := raw.(map[string]any)
			if check["name"] == "local_server" {
				found = check["code"] == "server_unreachable" && strings.Contains(check["fix"].(string), "aboard up")
			}
		}
		if !found {
			t.Fatalf("%s doctor has no local start guidance: %s", state, doctor)
		}
		text := e.runExit("doctor").stdout
		if !strings.Contains(text, "local server not running") || !strings.Contains(text, "aboard up") {
			t.Fatalf("%s doctor text: %s", state, text)
		}
		if e.run("status", "--json").json(t)["server_running"] != false {
			t.Fatalf("%s diagnostics started the server", state)
		}
	}
}

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
	person.run("board", "new", "another", "--server", second.url())
	ambiguous := person.runExit("status", "--json")
	if ambiguous.code != 1 || errorCode(t, ambiguous.json(t)) != "board_ambiguous" {
		t.Fatalf("multiple boards must require a choice: %s", ambiguous)
	}
	person.run("status", "--server", second.url(), "--board", "same", "--json")
}

func TestKnownServerWithoutDefaultRequiresSelection(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := tm.person("sam")
	path := filepath.Join(person.configDir(), "servers.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	delete(saved, "default")
	raw, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := person.runExit("boards", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_not_selected" {
		t.Fatalf("implicit only-server fallback: %s", r)
	}
	person.run("boards", "--server", tm.url(), "--json")
}

func TestInviteServerAndPersonHaveSeparateMeanings(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.admin.run("board", "new", "same", "--server", tm.url())
	code := tm.admin.run("invite", "--server", tm.url(), "--board", "same", "--json").json(t)
	if code["join_line"] == nil || code["link"] != nil {
		t.Fatalf("board invite became a person invite: %v", code)
	}
	person := tm.admin.run("invite", "--person", "--server", tm.url(), "--board", "same", "--json").json(t)
	if person["link"] == nil || person["join_line"] != nil {
		t.Fatalf("explicit person invite lost its meaning: %v", person)
	}
}
