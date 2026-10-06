//go:build e2e

package e2e

import (
	"slices"
	"strings"
	"testing"
)

// A machine that runs its own local server and is also connected to a team's server
// never guesses which one a person command is for (issue #136): with neither --server,
// .aboard nor a default, keys, people, invite and board new refuse and name both
// servers. aboard servers lists them, servers use picks the default, and every command
// names the server it acted on.
func TestPersonCommandsChooseAmongSeveralServers(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := newPersonHome(t, "maya")
	maya.run("up")
	local := "http://" + maya.addr

	connected := maya.run("connect", tm.invite(), "--json").json(t)
	matchesCLISpec(t, "ConnectOutput", connected)
	if field(t, connected, "default") != false {
		t.Fatalf("connect made the team's server the default without asking: %v", connected)
	}

	listed := maya.run("servers", "--json").json(t)
	matchesCLISpec(t, "ServersOutput", listed)
	if n := len(field(t, listed, "servers").([]any)); n != 2 || field(t, listed, "default") != nil {
		t.Fatalf("servers: %v", listed)
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
	key := maya.run("keys", "create", "browser", "--server", tm.url()).stdout
	if !strings.HasPrefix(key, `Key "browser" for `+tm.url()+" (shown once") {
		t.Fatalf("keys create --server:\n%s", key)
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
	created := maya.run("board", "new", "payments", "--json").json(t)
	if field(t, created, "server.url") != local {
		t.Fatalf("board new with the local server as the default: %v", created)
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
	if text := sam.run("servers").stdout; !strings.Contains(text, tm.url()) || strings.Contains(text, "No default") {
		t.Fatalf("servers with one server:\n%s", text)
	}
}
