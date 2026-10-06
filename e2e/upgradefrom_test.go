//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// upgradeFromVar names an aboard binary built from an older commit. TestUpgradeFromAnOlderBuild
// runs only when it is set; scripts/upgrade-rehearsal builds the older commit and this
// checkout and sets it.
const upgradeFromVar = "ABOARD_UPGRADE_FROM"

// rehearsal is one machine laid out as a person's: aboard installed in ~/.local/bin,
// its files in the XDG folders under HOME, and the local server on a port of its own.
type rehearsal struct {
	*env
	installed string
}

func (r *rehearsal) configDir() string { return filepath.Join(r.home, ".config", "aboard") }
func (r *rehearsal) dataDir() string   { return filepath.Join(r.home, ".local", "share", "aboard") }
func (r *rehearsal) stateDir() string  { return filepath.Join(r.home, ".local", "state", "aboard") }
func (r *rehearsal) serverPID() int    { return pidIn(filepath.Join(r.dataDir(), "server.pid")) }
func (r *rehearsal) daemonPID() int    { return pidIn(filepath.Join(r.stateDir(), "daemon.pid")) }

// newRehearsal makes the machine and installs older at ~/.local/bin/aboard.
func newRehearsal(t *testing.T, older string) *rehearsal {
	t.Helper()
	e := newEnv(t)
	// No ABOARD_HOME: the files go where a person's do.
	e.vars = slices.DeleteFunc(e.vars, func(v string) bool { return strings.HasPrefix(v, "ABOARD_HOME=") })
	r := &rehearsal{env: e, installed: filepath.Join(e.home, ".local", "bin", "aboard")}
	installAt(t, older, r.installed)
	e.bin = r.installed
	return r
}

// boardRecord is what must survive an upgrade: each board's messages as an agent on it
// reads them, and each agent's unread count.
type boardRecord struct {
	messages map[string][]string // board -> "seq from: body"
	unread   map[string]int      // agent -> unread messages
}

// record reads every board through one agent on it, and every agent's unread count.
func (r *rehearsal) record(boards map[string]string, agents []string) boardRecord {
	r.t.Helper()
	rec := boardRecord{messages: map[string][]string{}, unread: map[string]int{}}
	for board, reader := range boards {
		out := r.run("read", "--board", board, "--as", reader, "--limit", "200", "--json").json(r.t)
		for _, m := range field(r.t, out, "messages").([]any) {
			msg := m.(map[string]any)
			from, _ := field(r.t, msg, "from.name").(string)
			rec.messages[board] = append(rec.messages[board], fmt.Sprintf("%v %s: %v", msg["seq"], from, msg["body"]))
		}
	}
	for _, a := range agents {
		out := r.run("inbox", "--as", a, "--peek", "--json").json(r.t)
		rec.unread[a] = len(field(r.t, out, "messages").([]any))
	}
	return rec
}

// verify checks each board's hash chain.
func (r *rehearsal) verify(boards map[string]string) {
	r.t.Helper()
	for board, reader := range boards {
		if a := r.runExit("audit", "verify", "--board", board, "--as", reader, "--json"); a.code != 0 {
			r.t.Fatalf("audit of %s failed:\n%s", board, a)
		}
	}
}

// codexGot reports whether the fake codex was asked to queue a message containing text.
func (r *rehearsal) codexGot(text string) bool {
	for _, c := range r.fakeCodexCalls() {
		if strings.Contains(c["message"], text) {
			return true
		}
	}
	return false
}

// notOK returns doctor's checks that are not ok, as "name: level code message".
func (r *rehearsal) notOK() []string {
	r.t.Helper()
	out := r.runExit("doctor", "--json")
	var bad []string
	for _, c := range field(r.t, out.json(r.t), "checks").([]any) {
		m := c.(map[string]any)
		// This machine has no omp; the person's has, and doctor checks it there.
		if m["level"] != "ok" && m["code"] != "omp_not_installed" {
			bad = append(bad, fmt.Sprintf("%v: %v %v %v", m["name"], m["level"], m["code"], m["message"]))
		}
	}
	return bad
}

// A person running an older build upgrades with the install script while their agents
// work, keeps using their local boards, signs in to a team server, then rolls back.
// It rehearses that on one machine laid out as theirs: eight agents in Claude Code and
// Codex sessions on three local boards, with delivery running, a Claude Code session
// waiting for messages across the upgrade, and messages read and unread.
//
// It runs only when ABOARD_UPGRADE_FROM names the older aboard; scripts/upgrade-rehearsal
// builds it from a commit and runs this test.
func TestUpgradeFromAnOlderBuild(t *testing.T) {
	older := os.Getenv(upgradeFromVar)
	if older == "" {
		t.Skip(upgradeFromVar + " is not set; scripts/upgrade-rehearsal <commit> runs this")
	}
	r := newRehearsal(t, older)
	// The team server signed in to later. This machine trusts its certificate from the
	// start, as a machine trusts a team's certificate authority, so the delivery daemon
	// started now trusts it too.
	s := startTeamServer(t)
	r.vars = append(r.vars, "SSL_CERT_FILE="+s.certs)
	report := func(format string, args ...any) { t.Logf("REHEARSAL: "+format, args...) }
	report("older build: %s", strings.TrimSpace(r.run("version").stdout))

	// Before: the older aboard set up, three boards and eight agents.
	r.run("init", "--yes", "--allow-commands")
	writer, reviewer := r.claudeSession("s-writer"), r.claudeSession("s-reviewer")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	reviewer.run("join", line, "--name", "reviewer")

	builder := r.codexSession("019a0000-0000-7000-8000-0000000000a1")
	tester := r.claudeSession("s-tester")
	ops := r.codexSession("019a0000-0000-7000-8000-0000000000a2")
	line = field(t, builder.run("pair", "--new", "--board", "build", "--name", "builder", "--json").json(t), "join.line").(string)
	tester.run("join", line, "--name", "tester")
	ops.run("join", line, "--name", "ops")

	scribe := r.claudeSession("s-scribe")
	critic := r.codexSession("019a0000-0000-7000-8000-0000000000a3")
	planner := r.claudeSession("s-planner")
	line = field(t, scribe.run("pair", "--new", "--board", "notes", "--name", "scribe", "--json").json(t), "join.line").(string)
	critic.run("join", line, "--name", "critic")
	planner.run("join", line, "--name", "planner")

	boards := map[string]string{"writer-reviewer": "writer", "build": "builder", "notes": "scribe"}
	agents := []string{"writer", "reviewer", "builder", "tester", "ops", "scribe", "critic", "planner"}

	// Messages: some read, some waiting, some delivered to Codex by the daemon.
	writer.run("say", "--to", "@reviewer", "draft one is in notes.md")
	writer.run("say", "--to", "@reviewer", "draft two as well")
	reviewer.run("inbox") // reviewer has read both
	tester.run("say", "--to", "@builder", "build is red on main")
	tester.run("say", "--to", "@ops", "deploy paused")
	builder.run("say", "--to", "@tester", "looking at the build") // tester leaves it unread
	scribe.run("say", "--to", "@critic", "minutes from monday")
	scribe.run("say", "--to", "@planner", "plan for the week")
	eventually(t, 15*time.Second, "the older daemon to hand the Codex agents their messages", func() bool {
		return r.codexGot("build is red on main") && r.codexGot("deploy paused") && r.codexGot("minutes from monday")
	})

	// The reviewer's session is idle, its stop hook from the older aboard waiting.
	waiting := reviewer.startHook("stop")
	if !waiting.running(500 * time.Millisecond) {
		t.Fatalf("the older stop hook returned without a message\n%s", waiting.wait(time.Second))
	}
	oldServer, oldDaemon := r.serverPID(), r.daemonPID()
	if oldServer == 0 || oldDaemon == 0 {
		t.Fatalf("the older aboard runs no server (%d) or no daemon (%d)", oldServer, oldDaemon)
	}
	r.verify(boards)
	if bad := r.notOK(); len(bad) > 0 {
		report("doctor on the older build, not ok: %q", bad)
	}
	before := r.record(boards, agents)
	report("before: %d agents, unread %v", len(agents), before.unread)

	// The upgrade: the install script puts the new aboard in the same folder.
	newBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	rel := newInstallRelease(t)
	rel.publish([]archiveEntry{{name: "aboard", body: string(newBytes)}, {name: "LICENSE", body: "license\n"}})
	out, code := rel.run("HOME="+r.home, "ABOARD_INSTALL_DIR="+filepath.Dir(r.installed))
	if code != 0 {
		t.Fatalf("the install script failed:\n%s", out)
	}
	report("installed with the install script: %s", strings.TrimSpace(r.run("version").stdout))

	// What the person runs next.
	status := r.run("status", "--json").json(t)
	// The older daemon stops when its server is replaced, so status starts a new one
	// rather than replacing it.
	report("status after upgrade: replaced the server: %v", status["server_replaced"] != nil)
	r.run("init", "--yes", "--allow-commands")
	if np, nd := r.serverPID(), r.daemonPID(); np == oldServer || nd == oldDaemon || np == 0 || nd == 0 {
		t.Fatalf("after the upgrade the server is %d (was %d) and the daemon %d (was %d)", np, oldServer, nd, oldDaemon)
	}
	report("CHECK daemon and server restarted on the new build: pass")

	// The record.
	r.verify(boards)
	after := r.record(boards, agents)
	for board, msgs := range before.messages {
		if !slices.Equal(after.messages[board], msgs) {
			t.Fatalf("board %s changed across the upgrade:\nbefore %q\nafter  %q", board, msgs, after.messages[board])
		}
	}
	for a, n := range before.unread {
		if after.unread[a] != n {
			t.Fatalf("%s had %d unread before the upgrade and %d after", a, n, after.unread[a])
		}
	}
	report("CHECK boards, messages and read positions intact: pass")

	backups, _ := filepath.Glob(filepath.Join(r.dataDir(), "backups", "aboard-*-schema-*.db"))
	if len(backups) == 0 {
		t.Fatalf("no backup in %s after the upgrade migrated the database", filepath.Join(r.dataDir(), "backups"))
	}
	backup := backups[len(backups)-1]
	report("CHECK pre-migration backup: pass, %s", strings.TrimPrefix(backup, r.home+"/"))

	if bad := r.notOK(); len(bad) > 0 {
		report("doctor after upgrade, not ok: %q", bad)
		t.Errorf("doctor isn't clean after the upgrade: %q", bad)
	} else {
		report("CHECK doctor clean: pass")
	}
	if got := field(t, r.run("servers", "--json").json(t), "default.name"); got != "local" {
		t.Fatalf("aboard servers' default after the upgrade is %v, want local", got)
	}
	report("CHECK servers shows local as the default: pass")

	// Running sessions keep getting messages, without aboard resume.
	writer.run("say", "--to", "@reviewer", "after the upgrade, to the waiting session")
	if woke := waiting.wait(15 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "after the upgrade, to the waiting session") {
		t.Fatalf("the stop hook that waited across the upgrade wasn't woken with the new message\n%s", woke)
	}
	tester.run("say", "--to", "@builder", "after the upgrade, to builder")
	scribe.run("say", "--to", "@critic", "after the upgrade, to critic")
	eventually(t, 15*time.Second, "the new daemon to hand the Codex sessions new messages", func() bool {
		return r.codexGot("after the upgrade, to builder") && r.codexGot("after the upgrade, to critic")
	})
	builder.run("say", "--to", "@tester", "after the upgrade, to tester")
	if woke := tester.startHook("stop").wait(15 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "after the upgrade, to tester") {
		t.Fatalf("the tester's next stop hook wasn't woken with the waiting messages\n%s", woke)
	}
	report("CHECK registered sessions get new messages without resume: pass")
	if bad := r.notOK(); len(bad) > 0 {
		t.Errorf("doctor isn't clean after delivering: %q", bad)
	}

	// A team server: sign in, and the local boards and sessions carry on.
	adminKey, err := os.ReadFile(filepath.Join(s.data, "admin-key"))
	if err != nil {
		t.Fatal(err)
	}
	if lg := r.exec(nil, string(adminKey), "login", s.url, "--json"); lg.code != 0 {
		t.Fatalf("login to the team server:\n%s", lg)
	}
	listed := r.run("servers", "--json").json(t)
	if field(t, listed, "default.name") != "local" || len(field(t, listed, "servers").([]any)) != 2 {
		t.Fatalf("aboard servers after login: %v, want two servers with local the default", listed)
	}
	if got := field(t, r.run("board", "new", "loose-ends", "--json").json(t), "server.name"); got != "local" {
		t.Fatalf("board new without --server went to %v, want local", got)
	}
	if got := field(t, r.run("board", "new", "team-plans", "--server", s.url, "--json").json(t), "server.url"); got != s.url {
		t.Fatalf("board new --server %s went to %v", s.url, got)
	}
	var localBoards []string
	for _, b := range field(t, r.run("boards", "--all", "--json").json(t), "boards").([]any) {
		localBoards = append(localBoards, b.(map[string]any)["name"].(string))
	}
	if !slices.Contains(localBoards, "loose-ends") || slices.Contains(localBoards, "team-plans") {
		t.Fatalf("boards without --server lists %v, want the local boards only", localBoards)
	}
	// An agent on the team board, in a project of its own: a join links its directory.
	project := r.dir
	r.dir = filepath.Join(r.home, "team-project")
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	visitor := r.claudeSession("s-visitor")
	visitor.run("join", "--board", "team-plans", "--server", s.url, "--name", "visitor")
	visitor.run("say", "hello from the team board")
	if got := visitor.run("read", "--json").json(t); field(t, got, "messages.0.body") != "hello from the team board" {
		t.Fatalf("the team board doesn't show the visitor's message: %v", got)
	}
	r.dir = project
	writer.run("say", "--to", "@reviewer", "after the login, to reviewer")
	if woke := reviewer.startHook("stop").wait(15 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "after the login, to reviewer") {
		t.Fatalf("a local session wasn't woken after the login\n%s", woke)
	}
	tester.run("say", "--to", "@ops", "after the login, to ops")
	eventually(t, 15*time.Second, "a local Codex session to get a message after the login", func() bool {
		return r.codexGot("after the login, to ops")
	})
	if bad := r.notOK(); len(bad) > 0 {
		t.Errorf("doctor isn't clean after the login: %q", bad)
	}
	report("CHECK team server: login ok; local stays default; no --server goes to local; --server reaches the team; local sessions unaffected: pass")

	// Rollback: put the older aboard back first, so a waiting hook that starts the daemon
	// again starts the older one; then stop everything and restore the backup.
	since := r.record(boards, agents)
	idle := reviewer.startHook("stop") // the reviewer's session waits across the rollback
	if !idle.running(500 * time.Millisecond) {
		t.Fatalf("the stop hook returned without a message\n%s", idle.wait(time.Second))
	}
	installAt(t, older, r.installed)
	if d := r.runExit("down", "--json"); d.code != 0 {
		// This is the older build's down. One from before the fix waits for no daemon to
		// answer, and the waiting stop hook starts one again at once, so it gives up
		// after 10s; the server and the daemon it found have stopped by then, as the
		// recovery page says.
		if field(t, d.json(t), "error.code") != "daemon_not_running" {
			t.Fatalf("aboard down on the older build:\n%s", d)
		}
		report("rollback: the older build's aboard down gave up on a daemon the waiting hook started again: %v", field(t, d.json(t), "error.message"))
	}
	db := filepath.Join(r.dataDir(), "aboard.db")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("the local database isn't at %s: %v", db, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(db + suffix)
	}
	copyFile(t, backup, db)
	// The older build takes the one server in servers.json as its default (fixed in
	// #138), so its commands without --server would go to the team server. Moving the
	// file aside keeps the team login for the next upgrade.
	servers := filepath.Join(r.configDir(), "servers.json")
	if err := os.Rename(servers, servers+".team"); err != nil {
		t.Fatal(err)
	}
	report("rollback: reinstall older; aboard down; cp %s %s; rm aboard.db-wal aboard.db-shm; mv servers.json servers.json.team",
		strings.TrimPrefix(backup, r.home+"/"), strings.TrimPrefix(db, r.home+"/"))
	r.run("up")
	r.run("daemon", "start")
	report("rollback: aboard up; aboard daemon start")
	if st := r.runExit("status", "--json"); st.code != 0 {
		t.Fatalf("status on the older aboard after the rollback:\n%s", st)
	}
	r.verify(boards)
	rolled := r.record(boards, agents)
	for board, msgs := range before.messages {
		if !slices.Equal(rolled.messages[board], msgs) {
			t.Fatalf("board %s after the rollback:\nwant %q\ngot  %q", board, msgs, rolled.messages[board])
		}
	}
	lost := 0
	for board, msgs := range since.messages {
		lost += len(msgs) - len(rolled.messages[board])
	}
	report("rollback: boards as before the upgrade; %d messages written after the upgrade are gone; unread %v", lost, rolled.unread)
	r.run("init", "--yes", "--allow-commands") // the older skill back
	if bad := r.notOK(); len(bad) > 0 {
		report("doctor after rollback, not ok: %q", bad)
	}
	writer.run("say", "--to", "@reviewer", "after the rollback, to reviewer")
	if woke := idle.wait(15 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, "after the rollback, to reviewer") {
		report("rollback: the stop hook that waited across it ended without the message (exit %d): %s", woke.code, strings.TrimSpace(woke.stderr))
		t.Errorf("the session idle across the rollback wasn't woken\n%s", woke)
		if again := reviewer.startHook("stop").wait(15 * time.Second); again.code != 2 || !strings.Contains(again.stderr, "after the rollback, to reviewer") {
			t.Fatalf("the session's next stop hook wasn't woken either\n%s", again)
		}
	}
	tester.run("say", "--to", "@builder", "after the rollback, to builder")
	eventually(t, 15*time.Second, "a Codex session to get a message after the rollback", func() bool {
		return r.codexGot("after the rollback, to builder")
	})
	report("rollback: sessions get messages on the older build: pass")
	// Sequence numbers written after the upgrade are reused now; the record checks out.
	for board, reader := range boards {
		if a := r.runExit("audit", "verify", "--board", board, "--as", reader, "--json"); a.code != 0 {
			report("rollback: audit of %s after new messages fails:\n%s", board, a)
			t.Errorf("audit of %s after the rollback fails", board)
		}
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o600); err != nil { //nolint:gosec // a path in this test's home
		t.Fatal(err)
	}
}
