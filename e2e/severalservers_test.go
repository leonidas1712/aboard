//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoardsAcrossServersAndDefaultReads(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	person := first.person("sam")
	person.run("connect", second.invite())
	for _, url := range []string{first.url(), second.url()} {
		person.run("board", "new", "same", "--server", url)
	}
	// Aggregation is explicit; the default list stays on one issuer.
	defaultList := person.run("boards", "--json").json(t)
	if field(t, defaultList, "server.url") != first.url() || defaultList["servers"] != nil {
		t.Fatalf("default board list: %v", defaultList)
	}
	listed := person.run("boards", "--all-servers", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", listed)
	groups, ok := listed["servers"].([]any)
	if !ok || len(groups) != 2 {
		t.Fatalf("expected two server groups: %v", listed)
	}
	for _, group := range groups {
		g := group.(map[string]any)
		boards := field(t, g, "boards").([]any)
		if len(boards) != 1 || field(t, boards[0].(map[string]any), "name") != "same" {
			t.Fatalf("server group: %v", g)
		}
	}
	single := person.run("boards", "--server", first.url(), "--json").json(t)
	if field(t, single, "server.url") != first.url() || single["servers"] != nil {
		t.Fatalf("single server: %v", single)
	}
	if _, err := os.Stat(filepath.Join(person.dir, ".aboard")); !os.IsNotExist(err) {
		t.Fatalf("board creation wrote a folder link: %v", err)
	}
	person.run("servers", "use", second.url())
	status := person.run("status", "--board", "same", "--json").json(t)
	if field(t, status, "server.url") != second.url() {
		t.Fatalf("status ignored default: %v", status)
	}

	second.call("POST", "/v1/boards/same/messages", second.key(person), map[string]any{"to": []string{"all"}, "body": "default-server-message"})
	audit := person.run("audit", "verify", "--board", "same", "--json").json(t)
	if field(t, audit, "events_checked").(float64) != 3 {
		t.Fatalf("audit ignored default: %v", audit)
	}
	bare := person.run("status", "--json").json(t)
	if field(t, bare, "server.url") != second.url() {
		t.Fatalf("bare status ignored default: %v", bare)
	}
	w := person.watch("--board", "same", "--json")
	select {
	case line := <-w.stdout:
		var message map[string]any
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if field(t, message, "message.body") != "default-server-message" {
			t.Fatalf("watch ignored default: %s", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("watch did not show default server message")
	}
	w.stop()
	second.admin.run("down")
	partial := person.run("boards", "--all-servers", "--json").json(t)
	groups = field(t, partial, "servers").([]any)
	successes, failures := 0, 0
	for _, group := range groups {
		g := group.(map[string]any)
		if g["error"] != nil {
			failures++
		} else {
			successes++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("partial list: %v", partial)
	}
	if !strings.Contains(person.run("boards", "--all-servers").stdout, first.url()) {
		t.Fatal("partial text hid successful server")
	}
	first.admin.run("down")
	failed := person.runExit("boards", "--all-servers", "--json")
	if failed.code != 1 || len(field(t, failed.json(t), "servers").([]any)) != 2 {
		t.Fatalf("all unavailable: %s", failed)
	}
}

func TestBoardsExplicitLocalAndUnknownServer(t *testing.T) {
	t.Parallel()
	solo := newEnv(t)
	solo.run("pair")
	local := solo.run("boards", "--server", "local", "--json").json(t)
	if field(t, local, "server.url") != "http://"+solo.addr {
		t.Fatalf("local selector: %v", local)
	}
	badAgent := solo.runExit("boards", "--as", "missing", "--json")
	if badAgent.code != 1 || errorCode(t, badAgent.json(t)) != "agent_not_selected" {
		t.Fatalf("unknown agent fell back: %s", badAgent)
	}
	var calls atomic.Int32
	unknown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer unknown.Close()
	r := solo.runExit("boards", "--server", unknown.URL, "--json")
	if r.code != 1 || calls.Load() != 0 {
		t.Fatalf("unknown server received a request or was accepted: %s, calls %d", r, calls.Load())
	}
}

func TestExplicitServerBoardHintsStayOnTheirIssuer(t *testing.T) {
	t.Parallel()
	first, second := newTeam(t), newTeam(t)
	for _, tm := range []*team{first, second} {
		tm.admin.run("board", "new", "same")
		tm.admin.run("board", "new", "saved")
		tm.admin.run("board", "archive", "saved")
	}
	person := first.person("sam")
	person.run("connect", second.invite())
	project := map[string]any{"server": map[string]string{"name": first.url(), "url": first.url()}, "board": "same"}
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(person.dir, ".aboard"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	text := person.run("boards", "--server", second.url(), "--all").stdout
	joinHint := "aboard board add @me --server " + second.url() + " --board same"
	archiveHint := "aboard boards --archived --server " + second.url() + " --all"
	if !strings.Contains(text, "join with "+joinHint) || !strings.Contains(text, ": "+archiveHint) {
		t.Fatalf("hints lost issuer:\n%s", text)
	}
	person.run(strings.Fields(strings.TrimPrefix(joinHint, "aboard "))...)
	for _, tm := range []*team{first, second} {
		status, board := tm.call("GET", "/v1/boards/same", tm.key(person), nil)
		if status != http.StatusOK || field(t, board, "on_board") != (tm == second) {
			t.Fatalf("join hint acted on wrong server %s: %v", tm.url(), board)
		}
	}
	archived := person.run(append(strings.Fields(strings.TrimPrefix(archiveHint, "aboard ")), "--json")...).json(t)
	if field(t, archived, "server.url") != second.url() || field(t, archived, "boards.0.name") != "saved" {
		t.Fatalf("archive hint changed issuer: %v", archived)
	}
}
