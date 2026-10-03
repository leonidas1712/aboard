//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codexNoNetwork are the variables Codex sets for a command it runs in its sandbox with
// network access off, its default.
var codexNoNetwork = []string{
	"CODEX_THREAD_ID=019a0000-0000-7000-8000-000000000005",
	"CODEX_SANDBOX=seatbelt",
	"CODEX_SANDBOX_NETWORK_DISABLED=1",
}

// Codex's sandbox blocks network access by default, so a command run there can't reach
// the local server or the daemon's socket even when both run. Commands say so and name
// the fix, rather than reporting the server stopped or the daemon missing, and start
// nothing there. Here nothing runs, which is all a sandboxed command can see.
func TestCodexSandboxWithoutNetworkNamesTheCause(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer")
	e.run("down")
	sandboxed := &session{e: e, harness: "codex", id: "019a0000-0000-7000-8000-000000000005", vars: codexNoNetwork}

	for _, args := range [][]string{
		{"say", "--as", "writer", "hello", "--json"}, // reaches for the server
		{"inbox", "--json"},                          // reaches for the daemon to find its agent
	} {
		r := sandboxed.runExit(args...)
		if r.code != 1 || field(t, r.json(t), "error.code") != "sandbox_blocks_network" {
			t.Fatalf("want sandbox_blocks_network\n%s", r)
		}
		msg, hint := field(t, r.json(t), "error.message").(string), field(t, r.json(t), "error.hint").(string)
		if !strings.Contains(msg, "Codex's sandbox") || !strings.Contains(msg, "network") ||
			!strings.Contains(hint, "aboard init --yes --allow-commands") || !strings.Contains(hint, "approve") {
			t.Fatalf("the error should name the sandbox and the fix\n%s", r)
		}
	}

	status := sandboxed.run("status")
	for _, want := range []string{
		"Server: http://127.0.0.1:" + e.port() + " can't be reached from Codex's sandbox, which blocks network access; " +
			"run aboard init --yes --allow-commands in a terminal, or approve this command outside the sandbox\n",
		"Daemon: can't be reached from Codex's sandbox\n",
	} {
		if !strings.Contains(status.stdout, want) {
			t.Fatalf("status lacks %q:\n%s", want, status)
		}
	}
	if st := sandboxed.run("status", "--json").json(t); field(t, st, "sandbox_blocks_network") != true {
		t.Fatalf("status --json: %v", st)
	}

	checks := map[string]map[string]any{}
	for _, c := range field(t, sandboxed.runExit("doctor", "--json").json(t), "checks").([]any) {
		m := c.(map[string]any)
		checks[m["name"].(string)] = m
	}
	for _, name := range []string{"local_server", "daemon"} {
		if c := checks[name]; c["code"] != "sandbox_blocks_network" || !strings.Contains(c["fix"].(string), "aboard init --yes --allow-commands") {
			t.Fatalf("doctor's %s check inside the sandbox: %v", name, c)
		}
	}

	if e.daemonRunning() {
		t.Fatal("a daemon was started inside the sandbox")
	}
	if _, err := os.Stat(filepath.Join(e.dataDir(), "server.pid")); !os.IsNotExist(err) {
		t.Fatalf("a server was started inside the sandbox: %v", err)
	}

	// Outside the sandbox, and inside one that lets the command through, status says
	// what runs as before.
	e.run("up")
	e.run("daemon", "start")
	for _, r := range []result{e.run("status", "--json"), sandboxed.run("status", "--json")} {
		if st := r.json(t); field(t, st, "sandbox_blocks_network") != false || field(t, st, "server_running") != true {
			t.Fatalf("status: %v", st)
		}
	}
}

// aboard init recommends allowing aboard commands when it sets up Codex without the
// allow rule, doctor warns until the rule is there, and the rule allows the aboard
// command and nothing broader.
func TestCodexAllowRuleIsRecommendedAndChecked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.harnessHome()
	const recommend = "Codex's sandbox blocks network access, so aboard commands Codex runs can't reach the local server; " +
		"add --allow-commands to let Codex run them outside its sandbox.\n"

	if out := e.run("init", "--yes").stdout; !strings.Contains(out, recommend) {
		t.Fatalf("init without --allow-commands should recommend it:\n%s", out)
	}
	allow := codexAllowCheck(t, e)
	if allow["level"] != "warning" || allow["code"] != "codex_aboard_not_allowed" || allow["fix"] != "run aboard init --yes --allow-commands" {
		t.Fatalf("doctor without the rule: %v", allow)
	}

	if out := e.run("init", "--yes", "--allow-commands").stdout; strings.Contains(out, recommend) {
		t.Fatalf("init with --allow-commands still recommends it:\n%s", out)
	}
	rules := readFile(t, filepath.Join(e.home, ".codex", "rules", "aboard.rules"))
	if rules != "# Added by aboard init: run aboard commands without asking.\n"+
		`prefix_rule(pattern=["aboard"], decision="allow")`+"\n" {
		t.Fatalf("the rules file should allow the aboard command only:\n%s", rules)
	}
	if allow := codexAllowCheck(t, e); allow["level"] != "ok" || !strings.Contains(allow["message"].(string), "aboard commands run outside Codex's sandbox") {
		t.Fatalf("doctor with the rule: %v", allow)
	}
	if out := e.run("init", "--yes").stdout; strings.Contains(out, recommend) {
		t.Fatalf("init recommends a rule that is already there:\n%s", out)
	}

	// A project set up on its own gets the project's fix.
	p := newEnv(t)
	p.harnessHome()
	p.run("init", "--yes", "--scope", "project")
	if allow := codexAllowCheck(t, p); allow["fix"] != "run aboard init --yes --scope project --allow-commands in this project" {
		t.Fatalf("doctor for a project setup: %v", allow)
	}
}

// codexAllowCheck returns doctor's codex_allow check.
func codexAllowCheck(t *testing.T, e *env) map[string]any {
	t.Helper()
	for _, c := range field(t, e.runExit("doctor", "--json").json(t), "checks").([]any) {
		if m := c.(map[string]any); m["name"] == "codex_allow" {
			return m
		}
	}
	t.Fatal("doctor has no codex_allow check")
	return nil
}
