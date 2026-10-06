//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A machine that runs its own local server and then connects to a team's server keeps
// the local server as its default, so its commands carry on as before and name the
// server they acted on (issue #136). A machine that knows several servers with no
// default, such as one that connected before defaults existed, never guesses: keys,
// people, invite and board new refuse and name both servers. aboard servers lists them
// and servers use picks the default.
func TestPersonCommandsChooseAmongSeveralServers(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := newPersonHome(t, "maya")
	maya.run("up")
	local := "http://" + maya.addr

	connected := maya.run("connect", tm.invite(), "--json").json(t)
	matchesCLISpec(t, "ConnectOutput", connected)
	if field(t, connected, "default") != false {
		t.Fatalf("connect moved a machine that uses its local server to the team's server: %v", connected)
	}
	listed := maya.run("servers", "--json").json(t)
	matchesCLISpec(t, "ServersOutput", listed)
	if n := len(field(t, listed, "servers").([]any)); n != 2 || field(t, listed, "default.url") != local {
		t.Fatalf("servers after connect: %v", listed)
	}
	if text := maya.run("keys", "create", "laptop").stdout; !strings.Contains(text, " for "+local+" ") {
		t.Fatalf("keys create with the local server still the default:\n%s", text)
	}

	// A second machine of maya's that uses its local server says so when it logs in.
	key := field(t, maya.run("keys", "create", "desktop", "--server", tm.url(), "--json").json(t), "key.token").(string)
	desktop := newPersonHome(t, "maya")
	desktop.run("up")
	login := desktop.exec(nil, key+"\n", "login", tm.url())
	if login.code != 0 || !strings.Contains(login.stdout,
		"Your default stays the local server; use --server "+tm.url()+" or aboard servers use "+tm.url()+" to switch.\n") {
		t.Fatalf("login on a machine that uses its local server:\n%s", login)
	}

	// A machine that connected before defaults existed has none, and must choose.
	forgetDefault(t, maya)
	listed = maya.run("servers", "--json").json(t)
	if field(t, listed, "default") != nil {
		t.Fatalf("servers with the default forgotten: %v", listed)
	}
	text := maya.run("servers").stdout
	if !strings.Contains(text, local) || !strings.Contains(text, tm.url()) || !strings.Contains(text, "No default server") {
		t.Fatalf("servers:\n%s", text)
	}
	for _, args := range [][]string{
		{"keys", "create", "browser"},
		{"keys"},
		{"people"},
		{"invite", "--server"},
		{"board", "new", "payments"},
	} {
		r := maya.runExit(append(args, "--json")...)
		v := r.json(t)
		if r.code != 1 || errorCode(t, v) != "server_not_selected" {
			t.Fatalf("%v with two servers and no default:\n%s", args, r)
		}
		choices := field(t, v, "error.details.choices").([]any)
		if !slices.Contains(choices, any(local)) || !slices.Contains(choices, any(tm.url())) ||
			!strings.Contains(field(t, v, "error.hint").(string), "--server") {
			t.Fatalf("%v: the refusal doesn't name both servers and --server: %v", args, v)
		}
	}

	// --server chooses, and the output names the server.
	created := maya.run("keys", "create", "browser", "--server", tm.url()).stdout
	if !strings.HasPrefix(created, `Key "browser" for `+tm.url()+" (shown once") {
		t.Fatalf("keys create --server:\n%s", created)
	}
	for _, args := range [][]string{{"invite", "--server", tm.url(), "--json"}, {"invite", "--server=" + tm.url(), "--json"}} {
		inv := maya.runExit(args...)
		// maya is a member, so the team's server refuses her an invite: what matters is
		// which server was asked.
		if inv.code != 1 || errorCode(t, inv.json(t)) != "server_admin_required" {
			t.Fatalf("%v:\n%s", args, inv)
		}
	}
	if r := maya.runExit("invite", "--server", tm.url(), "extra"); r.code != 2 {
		t.Fatalf("invite --server with two arguments:\n%s", r)
	}

	// A default answers for the machine; --server and .aboard still win over it.
	used := maya.run("servers", "use", tm.url(), "--json").json(t)
	matchesCLISpec(t, "ServersUseOutput", used)
	if field(t, used, "server.url") != tm.url() || field(t, used, "previous") != nil {
		t.Fatalf("servers use: %v", used)
	}
	if text := maya.run("keys", "create", "phone").stdout; !strings.Contains(text, " for "+tm.url()+" ") {
		t.Fatalf("keys create with the team's server as the default:\n%s", text)
	}
	people := maya.run("people", "--json").json(t)
	if field(t, people, "server.url") != tm.url() {
		t.Fatalf("people with the team's server as the default: %v", people)
	}
	if got := maya.run("keys", "--server", local, "--json").json(t); field(t, got, "server.url") != local {
		t.Fatalf("keys --server local: %v", got)
	}
	if text := maya.run("servers").stdout; !strings.Contains(text, "* "+tm.url()) {
		t.Fatalf("servers doesn't mark the default:\n%s", text)
	}

	used = maya.run("servers", "use", "local", "--json").json(t)
	if field(t, used, "server.url") != local || field(t, used, "previous.url") != tm.url() {
		t.Fatalf("servers use local: %v", used)
	}
	board := maya.run("board", "new", "payments", "--json").json(t)
	if field(t, board, "server.url") != local {
		t.Fatalf("board new with the local server as the default: %v", board)
	}
	inv := maya.run("invite", "--json").json(t)
	matchesCLISpec(t, "InviteOutput", inv)
	if field(t, inv, "server.url") != local || field(t, inv, "board") != "payments" {
		t.Fatalf("invite: %v", inv)
	}

	r := maya.runExit("servers", "use", "https://elsewhere.example.com", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_unknown" {
		t.Fatalf("servers use with an unknown server:\n%s", r)
	}
}

// One server is enough to choose: a solo machine's commands act on its local server, and
// a machine connected only to a team's server acts on that one, without --server.
func TestOneKnownServerNeedsNoChoice(t *testing.T) {
	t.Parallel()
	solo := newEnv(t)
	solo.run("up")
	if text := solo.run("keys", "create", "phone").stdout; !strings.Contains(text, " for http://"+solo.addr+" ") {
		t.Fatalf("keys create on a solo machine:\n%s", text)
	}

	tm := newTeam(t)
	sam := tm.person("sam")
	if got := sam.run("keys", "--json").json(t); field(t, got, "server.url") != tm.url() {
		t.Fatalf("keys on a machine connected only to a team's server: %v", got)
	}
	if text := sam.run("servers").stdout; !strings.Contains(text, "* "+tm.url()) {
		t.Fatalf("servers on a machine whose only server is a team's:\n%s", text)
	}
}

// forgetDefault removes the default server from a home's servers.json, as on a machine
// that connected before defaults existed.
func forgetDefault(t *testing.T, e *env) {
	t.Helper()
	path := filepath.Join(e.configDir(), "servers.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	delete(v, "default")
	if raw, err = json.Marshal(v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
