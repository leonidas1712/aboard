//go:build e2e

package e2e

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// withoutAddr makes the env leave the local server's address to its ABOARD_HOME.
func (e *env) withoutAddr() {
	e.vars = slices.DeleteFunc(e.vars, func(kv string) bool { return strings.HasPrefix(kv, "ABOARD_LOCAL_ADDR=") })
	e.addr = ""
}

// recordedAddr is the address the env's ABOARD_HOME recorded for its local server.
func (e *env) recordedAddr() string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dataDir(), "server.addr"))
	if err != nil {
		e.t.Fatalf("no address recorded in ABOARD_HOME: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

// Two copies of Aboard, each with its own ABOARD_HOME and no address given, run side by
// side: each home picks a port of its own and keeps it, runs its own server and delivery
// daemon, and never sees the other's boards.
func TestTwoAboardHomesRunSideBySide(t *testing.T) {
	t.Parallel()
	homes := []*env{newEnv(t), newEnv(t)}
	for _, e := range homes {
		e.withoutAddr()
		line := e.run("pair", "--json").json(t)
		e.addr = e.recordedAddr()
		if _, port, err := net.SplitHostPort(e.addr); err != nil || port == "7400" {
			t.Fatalf("recorded address %q, want a free port of the home's own", e.addr)
		}
		if got := field(t, line, "server.url"); got != "http://"+e.addr {
			t.Fatalf("pair used server %v, want the home's http://%s", got, e.addr)
		}
		if got := field(t, line, "join.line").(string); !strings.Contains(got, " on localhost:"+e.port()+" as ") {
			t.Fatalf("join line %q doesn't name the home's port %s", got, e.port())
		}
		e.run("join", field(t, line, "join.line").(string))
		e.run("daemon", "start")
	}
	a, b := homes[0], homes[1]
	if a.addr == b.addr {
		t.Fatalf("both homes use %s", a.addr)
	}
	if pa, pb := pidIn(filepath.Join(a.stateDir(), "daemon.pid")), pidIn(filepath.Join(b.stateDir(), "daemon.pid")); pa == 0 || pb == 0 || pa == pb {
		t.Fatalf("daemon pids %d and %d, want one daemon per home", pa, pb)
	}
	if pa, pb := a.serverPID(), b.serverPID(); pa == 0 || pb == 0 || pa == pb {
		t.Fatalf("server pids %d and %d, want one server per home", pa, pb)
	}

	// Every later command in a home finds its server at the recorded address.
	for _, e := range homes {
		if status := e.run("status").stdout; !strings.Contains(status, "Server: http://"+e.addr+" running") {
			t.Fatalf("status in %s:\n%s", e.aboardHome(), status)
		}
		if doctor := e.runExit("doctor"); !strings.Contains(doctor.stdout, "local server running at http://"+e.addr) {
			t.Fatalf("doctor in %s:\n%s", e.aboardHome(), doctor)
		}
	}

	// A message in one home never reaches the other.
	a.run("say", "--as", "member", "--to", "@member-2", "Only for home A.")
	if got := field(t, a.run("inbox", "--as", "member-2", "--json").json(t), "messages.0.body"); got != "Only for home A." {
		t.Fatalf("home A's inbox: %v", got)
	}
	if msgs := field(t, b.run("inbox", "--as", "member-2", "--json").json(t), "messages").([]any); len(msgs) != 0 {
		t.Fatalf("home B's inbox has home A's messages: %v", msgs)
	}

	// A join line from one home names a server the other doesn't know.
	other := a.run("pair", "--new", "--json").json(t)
	r := b.runExit("join", "--json", field(t, other, "join.line").(string))
	if r.code == 0 || !strings.Contains(r.stdout, `"server_unknown"`) {
		t.Fatalf("joining home A's board from home B:\n%s", r)
	}

	// A restarted home comes back on the same address.
	a.run("down")
	expectLines(t, a.run("up"), "Started local Aboard at http://"+a.addr)
}
