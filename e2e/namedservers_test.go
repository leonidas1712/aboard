//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamedServersKeepIssuerAndAllowAgentBookkeeping(t *testing.T) {
	tm, other := newTeam(t), newTeam(t)
	person := newPersonHome(t, "sam")
	person.run("connect", tm.invite(), "--server-name", "work")
	invite := other.invite()
	refused := person.runExit("connect", invite, "--server-name", "WORK", "--json")
	if refused.code != 1 || errorCode(t, refused.json(t)) != "server_name_taken" {
		t.Fatalf("connect collision: %s", refused)
	}
	person.run("connect", invite, "--server-name", "elsewhere")
	listing := person.run("servers", "--json").json(t)
	matchesCLISpec(t, "ServersOutput", listing)
	raw, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "abh_") || strings.Contains(string(raw), "key_id") {
		t.Fatalf("server listing exposed credentials: %s", raw)
	}
	person.run("servers", "name", tm.url(), "work")

	named := person.run("servers", "rename", "work", "fly", "--json").json(t)
	matchesCLISpec(t, "ServersNameOutput", named)
	if field(t, named, "server.url") != tm.url() {
		t.Fatalf("rename moved issuer: %v", named)
	}
	person.run("board", "new", "named", "--server", "fly")
	opened := person.openUI("true", "fly").json(t)
	if field(t, opened, "server.url") != tm.url() || field(t, opened, "server_selection.source") != "flag" {
		t.Fatalf("positional open: %v", opened)
	}
	if field(t, opened, "server_selection.server.name") != "fly" {
		t.Fatalf("selection name: %v", opened)
	}
	project := person.openUI("true").json(t)
	if field(t, project, "server_selection.source") != "default" || field(t, project, "server.url") != tm.url() {
		t.Fatalf("project: %v", project)
	}
	badOpen := person.openUI("true", "fly", "--server", "elsewhere")
	if badOpen.code != 2 {
		t.Fatalf("conflicting open: %s", badOpen)
	}
	person.run("servers", "use", "elsewhere")
	if _, err := os.Stat(filepath.Join(person.dir, ".aboard")); !os.IsNotExist(err) {
		t.Fatalf("board creation wrote a folder link: %v", err)
	}
	def := person.openUI("true").json(t)
	if field(t, def, "server.url") != other.url() || field(t, def, "server_selection.source") != "default" {
		t.Fatalf("default: %v", def)
	}

	listed := person.run("boards", "--server", "fly", "--json").json(t)
	if field(t, listed, "server.url") != tm.url() {
		t.Fatalf("name selected wrong server: %v", listed)
	}
	otherList := person.run("boards", "--server", "elsewhere", "--json").json(t)
	if len(field(t, otherList, "boards").([]any)) != 0 {
		t.Fatalf("name creation crossed issuer: %v", otherList)
	}
	if text := person.run("boards", "--server", "fly").stdout; !strings.Contains(text, "fly") {
		t.Fatalf("name missing: %s", text)
	}
	conflict := person.runExit("servers", "name", "elsewhere", "fly", "--json")
	if conflict.code != 1 || errorCode(t, conflict.json(t)) != "server_name_taken" {
		t.Fatalf("collision: %s", conflict)
	}
	agentEnv := []string{"ABOARD_AGENT=unselected"}
	for _, args := range [][]string{{"servers", "--json"}, {"servers", "rename", "elsewhere", "other", "--json"}} {
		r := person.exec(agentEnv, "", args...)
		if r.code != 0 {
			t.Fatalf("agent bookkeeping: %s", r)
		}
	}
	for _, args := range [][]string{{"servers", "use", "fly", "--json"}, {"login", "fly", "--json"}, {"connect", tm.url(), "--json"}} {
		r := person.exec(agentEnv, "", args...)
		if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
			t.Fatalf("person boundary: %s", r)
		}
	}
}

func TestLocalServerNameKeepsLocalAlias(t *testing.T) {
	e := newEnv(t)
	e.run("up")
	e.run("servers", "name", "local", "laptop")
	list := e.run("servers", "--json").json(t)
	if field(t, list, "default.name") != "laptop" {
		t.Fatalf("local label: %v", list)
	}
	for _, value := range []string{"local", "laptop"} {
		opened := e.openUI("true", value).json(t)
		if field(t, opened, "server.url") != "http://"+e.addr {
			t.Fatalf("local alias moved: %v", opened)
		}
	}
	bad := e.runExit("servers", "rename", "laptop", "bad/name", "--json")
	if bad.code != 1 || errorCode(t, bad.json(t)) != "invalid_server_name" {
		t.Fatalf("invalid name: %s", bad)
	}
	unknown := e.openUI("true", "unknown")
	if unknown.code != 1 || errorCode(t, unknown.json(t)) != "server_unknown" {
		t.Fatalf("unknown: %s", unknown)
	}
}
