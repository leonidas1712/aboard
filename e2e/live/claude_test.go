//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// wakeBound is how soon an idle session must be handed a message once it is posted.
const wakeBound = 2 * time.Second

// checkWake fails the test if the session was handed the message later than wakeBound.
func checkWake(t *testing.T, posted message, wake handover) {
	t.Helper()
	if d := wake.Time.Sub(posted.At); d > wakeBound {
		t.Errorf("the idle session was handed message #%d %s after it was posted; want within %s", posted.Seq, d, wakeBound)
	}
}

// Two Claude Code sessions pair the way the quickstart shows: the first is asked in
// plain words, the second gets the join line it gives. Then a message to the first,
// sitting idle, wakes it and it replies on the board with no one typing.
func TestIdleClaudeWakesAndReplies(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	writer := l.startClaude("writer", l.project("writer-project", "claude-code"))
	reviewer := l.startClaude("reviewer", l.project("reviewer-project", "claude-code"))

	writer.submit("Pair with another agent on Aboard.")
	var line string
	l.waitFor(3*time.Minute, "the writer session to give a join line", func() bool {
		line = joinLine.FindString(writer.screen())
		return line != ""
	})
	writer.waitIdle(2 * time.Minute)
	reviewer.submit(line)
	// Agents are named after their harness: the writer is claude, the reviewer claude-2.
	l.waitMessage("claude-2", time.Time{}, "", 3*time.Minute) // its hello
	l.waitQuiet(4*time.Minute, "claude-2", writer, reviewer)
	l.waitPresence("claude", "idle", 30*time.Second)

	seen := l.watchPresence("claude")
	ping := l.say("claude-2", "--to", "@claude", "--expect-reply", "Reply to this message with exactly PONG-1.")
	wake := l.waitHanded(ping.At)
	reply := l.waitMessage("claude", ping.At, "PONG-1", 2*time.Minute)
	writer.waitIdle(2 * time.Minute)
	l.waitPresence("claude", "idle", 30*time.Second)
	if states := seen(); !slices.Contains(states, "working") {
		t.Errorf("the writer's presence during its reply turn went %v; want working on the way", states)
	}
	t.Logf("measured: handed %s after posting, reply on the board %s after posting",
		wake.Time.Sub(ping.At), reply.At.Sub(ping.At))
	checkWake(t, ping, wake)
	if reply.ReplyToSeq == nil || *reply.ReplyToSeq != ping.Seq {
		t.Errorf("the writer's PONG-1 (#%d) isn't a reply to #%d", reply.Seq, ping.Seq)
	}
}

// After one prompt, two Claude Code sessions run the skill's wiring check to PING 3:
// six messages go back and forth with no one typing, and then they stop.
func TestClaudeExchangesFiveMessages(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	proj := l.project("project", "claude-code")
	writer := l.startClaude("writer", proj)
	reviewer := l.startClaude("reviewer", proj)
	writer.bind("writer")
	reviewer.bind("reviewer")

	start := time.Now()
	writer.submit("Run the Aboard wiring check with @reviewer now, going up to PING 3 instead of PING 2.")
	l.waitMessage("reviewer", start, "PONG 3", 5*time.Minute)
	l.waitQuiet(3*time.Minute, "reviewer", writer, reviewer)
	exchangedAlone(t, l.messages("reviewer"), start, 5, 8)
	if l.typed != 3 {
		t.Errorf("%d prompts were typed; want the two first prompts and the one asking for the check", l.typed)
	}
}

// exchangedAlone checks the messages after start go back and forth between two agents,
// at least min of them and at most max, so the exchange ended rather than looping.
func exchangedAlone(t *testing.T, msgs []message, start time.Time, least, most int) {
	t.Helper()
	var after []message
	for _, m := range msgs {
		if !m.At.Before(start) {
			after = append(after, m)
		}
	}
	var log strings.Builder
	for _, m := range after {
		fmt.Fprintf(&log, "  #%d %s: %s\n", m.Seq, m.From.Name, m.Body)
	}
	t.Logf("the exchange:\n%s", log.String())
	if len(after) < least || len(after) > most {
		t.Fatalf("%d messages after the prompt; want %d to %d", len(after), least, most)
	}
	for i := 1; i < len(after); i++ {
		if after[i].From.Name == after[i-1].From.Name {
			t.Errorf("#%d and #%d both come from %s; the agents should take turns", after[i-1].Seq, after[i].Seq, after[i].From.Name)
		}
	}
}

// A message from the agent's owner reaches a busy Claude Code session at its next tool
// boundary and is acted on in that turn, while peer messages sent at the same time, an
// urgent one too, wait for the turn to end and then arrive together, as one bundle.
func TestOwnerReachesBusyClaude(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	board := l.pairCLI()
	proj := l.project("project", "claude-code")
	writeSlowTask(t, proj, 25)
	writer := l.startClaude("writer", proj)
	writer.bind("writer")

	writer.submit("Run `./" + slowTask + "` in the foreground, not as a background task, and wait for it. Then reply DONE.")
	l.waitFor(2*time.Minute, "the writer session to run the slow task", func() bool { return pgrep("sleep 25") })
	sent := time.Now()
	own := l.postAsOwner(board, "@writer", `OWNER: run aboard say "OWNER-ACK" right away, then carry on with your task.`)
	l.say("reviewer", "--to", "@writer", "--urgent", "PEER 1 of 3 (urgent): no action needed.")
	l.say("reviewer", "--to", "@writer", "PEER 2 of 3: no action needed.")
	l.say("reviewer", "--to", "@writer", `PEER 3 of 3: run aboard say "PEER-ACK".`)

	ownerAck := l.waitMessage("writer", sent, "OWNER-ACK", 3*time.Minute)
	peerAck := l.waitMessage("writer", sent, "PEER-ACK", 3*time.Minute)
	writer.waitIdle(2 * time.Minute)
	at, how, ok := l.reached(own.Seq)
	handed := l.handedAfter(sent)
	t.Logf("measured: the owner's message reached the turn %s after posting (%s); OWNER-ACK %s, PEER-ACK %s after sending; %d bundles handed",
		at.Sub(own.At), how, ownerAck.At.Sub(sent), peerAck.At.Sub(sent), len(handed))
	if !ok || how != "tool boundary" {
		t.Fatalf("the owner's message reached the session by %q; want at a tool boundary of the busy turn", how)
	}
	if len(handed) == 0 {
		t.Fatal("no bundle was handed after the messages were sent")
	}
	if handed[0].Time.Before(ownerAck.At) {
		t.Errorf("a bundle was handed at %s, before the owner's message was acted on in the busy turn (%s): "+
			"peer messages must wait for the turn to end", handed[0].Time, ownerAck.At)
	}
	var beforeAck int
	for _, h := range handed {
		if h.Time.Before(peerAck.At) {
			beforeAck++
		}
	}
	if beforeAck != 1 {
		t.Errorf("%d bundles were handed before PEER-ACK; want the three peer messages in one", beforeAck)
	}
	l.waitFor(30*time.Second, "every message to be acknowledged", func() bool { return len(l.writerInbox()) == 0 })
}

// A peer's message to a busy Claude Code session never enters the turn: at the next
// tool boundary only a notice names it, once, and the message itself arrives when the
// turn ends.
func TestPeerWaitsButNoticeArrives(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	proj := l.project("project", "claude-code")
	writeSlowTask(t, proj, 21)
	writer := l.startClaude("writer", proj)
	writer.bind("writer")

	writer.submit("Run `./" + slowTask + "` in the foreground, not as a background task, and wait for it. When it has finished, run `./" +
		slowTask + "` again the same way. Then reply DONE.")
	l.waitFor(2*time.Minute, "the writer session to run the slow task", func() bool { return pgrep("sleep 21") })
	peer := l.say("reviewer", "--to", "@writer", `PEER: run aboard say "PEER-ACK".`)
	ack := l.waitMessage("writer", peer.At, "PEER-ACK", 5*time.Minute)
	writer.waitIdle(2 * time.Minute)

	var notices []handover
	for _, h := range l.logged("tool boundary") {
		if slices.Contains(h.Announced, peer.Seq) {
			notices = append(notices, h)
		}
		if slices.Contains(h.Seqs, peer.Seq) {
			t.Errorf("the peer's message was added to the busy turn at %s", h.Time)
		}
	}
	at, how, _ := l.reached(peer.Seq)
	t.Logf("measured: %d notices named #%d; the message reached the session %s after posting (%s); PEER-ACK %s after posting",
		len(notices), peer.Seq, at.Sub(peer.At), how, ack.At.Sub(peer.At))
	if len(notices) != 1 {
		t.Fatalf("%d tool boundaries named the peer's message; want exactly one notice", len(notices))
	}
	// Two outcomes are right. The agent acts on the notice and fetches the message
	// itself with aboard inbox, which acknowledges it, so it is never handed at all; or
	// it waits, and the message is handed in a bundle once the busy turn ends, after the
	// notice. Either way it must never be handed twice.
	switch how {
	case "":
		if inbox := l.writerInbox(); len(inbox) != 0 {
			t.Errorf("the peer's message was neither handed nor acknowledged: the writer's inbox still holds %v", inbox)
		}
		t.Logf("the writer fetched the message itself after the notice")
	case "bundle handed":
		if at.Before(notices[0].Time) {
			t.Errorf("the peer's message was handed at %s, before the notice (%s); want it only after the turn ended", at, notices[0].Time)
		}
	default:
		t.Errorf("the peer's message reached the session by %q at %s; want a bundle after the notice, or the writer's own aboard inbox", how, at)
	}
	handedTimes := 0
	for _, h := range l.handed() {
		if h.Error == "" && slices.Contains(h.Seqs, peer.Seq) {
			handedTimes++
		}
	}
	if handedTimes > 1 {
		t.Errorf("the peer's message was handed %d times; want at most once", handedTimes)
	}
}

// With delivery mode humans, a peer's message leaves an idle Claude Code session
// asleep; its owner's message wakes it, and that bundle carries the peer's message too.
func TestHumansModeWakesOnlyForPeople(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	board := l.pairCLI()
	writer := l.startClaude("writer", l.project("project", "claude-code"))
	writer.bind("writer")
	l.run("delivery", "humans", "--as", "writer")

	note := l.say("reviewer", "--to", "@writer", "NOTE: no action needed.")
	l.neverWithin(10*time.Second, "a peer's message woke the session", func() bool { return len(l.handedAfter(note.At)) > 0 })
	if !slices.ContainsFunc(l.writerInbox(), func(m message) bool { return m.Seq == note.Seq }) {
		t.Fatalf("the peer's message #%d should still be unread", note.Seq)
	}

	own := l.postAsOwner(board, "@writer",
		`Run: aboard say "SEEN" followed by the sequence number of every message in this delivery, separated by spaces.`)
	wake := l.waitHanded(own.At)
	reply := l.waitMessage("writer", own.At, "SEEN", 2*time.Minute)
	t.Logf("measured: handed %s after the owner's message; reply %q", wake.Time.Sub(own.At), reply.Body)
	checkWake(t, own, wake)
	words := strings.Fields(reply.Body)
	for _, seq := range []int{note.Seq, own.Seq} {
		if !slices.Contains(words, strconv.Itoa(seq)) && !slices.Contains(words, "#"+strconv.Itoa(seq)) {
			t.Errorf("the reply %q doesn't name #%d; the owner's bundle should carry the peer's message too", reply.Body, seq)
		}
	}
	l.waitFor(30*time.Second, "both messages to be acknowledged", func() bool { return len(l.writerInbox()) == 0 })
}

// A person installs a new aboard over the old one while a Claude Code session waits.
// Without restarting the session, messages still arrive and get answers, the new aboard
// replaces the old daemon and server without handing a message twice, and the hook files
// stay byte for byte the same.
func TestUpgradeWithSessionOpen(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	const oldVersion = "0.0.1"
	old := filepath.Join(t.TempDir(), "aboard")
	if err := buildAboard(t.Context(), old, oldVersion); err != nil {
		t.Fatal(err)
	}
	l := newLabWith(t, old)
	l.pairCLI()
	proj := l.project("project", "claude-code")
	settings := filepath.Join(proj, ".claude", "settings.local.json")
	hooksBefore := readFile(t, settings)
	writer := l.startClaude("writer", proj)
	writer.bind("writer")
	if v := l.serverVersion(); v != oldVersion {
		t.Fatalf("the old local server reports version %q, want %q", v, oldVersion)
	}
	oldDaemon := l.daemonPID()

	staged := l.bin + ".new"
	copyFile(t, newBinary, staged)
	if err := os.Rename(staged, l.bin); err != nil {
		t.Fatal(err)
	}
	installed := time.Now()

	for i := 1; i <= 2; i++ {
		want := fmt.Sprintf("PONG-%d", i)
		ping := l.say("reviewer", "--to", "@writer", "--expect-reply", "Reply to this message with exactly "+want+".")
		reply := l.waitMessage("writer", ping.At, want, 3*time.Minute)
		t.Logf("measured: message %d answered %s after posting", i, reply.At.Sub(ping.At))
		writer.waitIdle(2 * time.Minute)
	}

	if n := len(l.handedAfter(installed)); n != 2 {
		t.Errorf("%d bundles handed for the two messages after the upgrade; each should be handed once", n)
	}
	if pid := l.daemonPID(); pid == 0 || pid == oldDaemon {
		t.Errorf("daemon pid %d; the old daemon (pid %d) should have been replaced", pid, oldDaemon)
	}
	if v := l.serverVersion(); v == oldVersion {
		t.Errorf("the local server still reports the old version %q", v)
	}
	for _, c := range l.doctor(proj) {
		// Only Claude Code is set up here. Codex's global skill is in the person's own
		// ~/.agents/skills, which CODEX_HOME doesn't move, so its checks say nothing here.
		if strings.HasPrefix(c.Name, "codex") {
			continue
		}
		if c.Code != nil && slices.Contains([]string{"daemon_outdated", "server_outdated", "hooks_outdated", "skill_outdated", "hooks_edited", "skill_edited"}, *c.Code) {
			t.Errorf("after the upgrade, doctor reports %s: %s", *c.Code, c.Message)
		}
		if c.Name == "daemon" && c.Level != "ok" {
			t.Errorf("after the upgrade, doctor's daemon check: %s %s", c.Level, c.Message)
		}
	}
	if !bytes.Equal(hooksBefore, readFile(t, settings)) {
		t.Errorf("%s changed during the upgrade", settings)
	}
	var again struct {
		Harnesses []struct {
			Changes []struct {
				Path   string `json:"path"`
				Action string `json:"action"`
			} `json:"changes"`
		} `json:"harnesses"`
	}
	l.decode(proj, &again, "init", "--yes", "--scope", "project", "--harness", "claude-code", "--allow-commands")
	for _, h := range again.Harnesses {
		for _, c := range h.Changes {
			if c.Action != "unchanged" {
				t.Errorf("aboard init after the upgrade would %s %s; the hooks didn't change, so nothing should", c.Action, c.Path)
			}
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// aboard init --yes --scope project sets up only the project: a Claude Code session
// started there runs Aboard's hooks, one started elsewhere doesn't, doctor names the
// project's settings file, and the person's own config is untouched.
func TestProjectScopeInit(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	proj := l.project("project", "claude-code")
	settings := filepath.Join(proj, ".claude", "settings.local.json")
	var installed struct {
		Hooks       map[string]json.RawMessage `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(readFile(t, settings), &installed); err != nil {
		t.Fatalf("%s: %v", settings, err)
	}
	if !strings.Contains(string(installed.Hooks["Stop"]), l.bin+" hook claude-code stop") {
		t.Errorf("the project's Stop hook doesn't run %s: %s", l.bin, installed.Hooks["Stop"])
	}
	if !slices.Contains(installed.Permissions.Allow, "Bash(aboard *)") {
		t.Errorf("the project's settings don't allow aboard commands: %v", installed.Permissions.Allow)
	}
	readFile(t, filepath.Join(proj, ".claude", "skills", "aboard", "SKILL.md"))

	l.startClaude("in-project", proj)
	l.waitFor(30*time.Second, "the session-start hook to open a session", func() bool { return l.openSessions() == 1 })
	checks := map[string]check{}
	for _, c := range l.doctor(proj) {
		checks[c.Name] = c
	}
	if c := checks["claude_hooks"]; c.Level != "ok" || !strings.Contains(c.Message, settings) {
		t.Errorf("doctor in the project should find the hooks in %s: %+v", settings, c)
	}
	if c := checks["claude_skill"]; c.Level != "ok" {
		t.Errorf("doctor in the project should find the skill: %+v", c)
	}
	var status struct {
		Setup struct {
			Project []string `json:"project"`
		} `json:"setup"`
	}
	l.decode(proj, &status, "status")
	if !slices.Contains(status.Setup.Project, "claude-code") {
		t.Errorf("status in the project: setup.project = %v, want claude-code", status.Setup.Project)
	}

	elsewhere := filepath.Join(l.dir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o750); err != nil {
		t.Fatal(err)
	}
	l.startClaude("elsewhere", elsewhere)
	l.neverWithin(5*time.Second, "a session outside the project ran Aboard's hooks", func() bool { return l.openSessions() > 1 })
	if diff := configDiff(realConfig, configSums()); diff != "" {
		t.Errorf("setting up a project changed the person's own config:\n%s", diff)
	}
}

// A Claude Code session killed after a wake, before its turn ends, never confirms the
// bundle: the message stays unread, the daemon closes the session within 5 seconds, and
// the next session that resumes the agent receives it and acts on it.
func TestKilledSessionRedelivers(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	proj := l.project("project", "claude-code")
	writeSlowTask(t, proj, 23)
	first := l.startClaude("first", proj)
	first.bind("writer")
	open := l.openSessions()

	job := l.say("reviewer", "--to", "@writer",
		"Run `./"+slowTask+"` in the foreground, not as a background task, and wait for it. Then run: aboard say \"KILLTEST-DONE\".")
	l.waitFor(2*time.Minute, "the woken session to run the slow task", func() bool { return pgrep("sleep 23") })
	if err := syscall.Kill(first.pid(), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	killed := time.Now()
	l.waitFor(15*time.Second, "the daemon to close the killed session", func() bool { return l.openSessions() == open-1 })
	t.Logf("measured: the daemon closed the killed session %s after the kill", time.Since(killed))
	// The daemon looks for gone harness processes every 5 seconds; the extra second is
	// this poll and the status command.
	if d := time.Since(killed); d > 6*time.Second {
		t.Errorf("the daemon closed the killed session after %s; want within 5 seconds", d)
	}
	_ = command(t.Context(), "pkill", "-x", "-f", "sleep 23").Run()
	if !slices.ContainsFunc(l.writerInbox(), func(m message) bool { return m.Seq == job.Seq }) {
		t.Fatalf("message #%d was acknowledged though the session that woke for it was killed before confirming", job.Seq)
	}

	second := l.startClaude("second", proj)
	second.bind("writer")
	l.waitMessage("writer", job.At, "KILLTEST-DONE", 3*time.Minute)
	if n := len(l.handedAfter(job.At)); n < 2 {
		t.Errorf("%d bundles handed after the message; want one to the killed session and one to the next", n)
	}
}

// Stopping the delivery daemon while a Claude Code session waits, and separately the
// local server, loses nothing: the waiting stop hook starts the daemon again, the server
// comes back with aboard up, and each message sent afterwards gets its answer.
func TestRestartsLoseNothing(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	writer := l.startClaude("writer", l.project("project", "claude-code"))
	writer.bind("writer")

	oldDaemon := l.daemonPID()
	if err := syscall.Kill(oldDaemon, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	l.waitFor(30*time.Second, "the waiting stop hook to start the daemon again", func() bool {
		pid := l.daemonPID()
		return pid != 0 && pid != oldDaemon && syscall.Kill(pid, 0) == nil
	})
	ping := l.say("reviewer", "--to", "@writer", "--expect-reply", "Reply to this message with exactly PONG-1.")
	reply := l.waitMessage("writer", ping.At, "PONG-1", 3*time.Minute)
	t.Logf("measured: after the daemon restart, answered %s after posting", reply.At.Sub(ping.At))
	writer.waitIdle(2 * time.Minute)

	server := l.serverPID()
	if err := syscall.Kill(server, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	l.waitFor(15*time.Second, "the local server to stop", func() bool { return syscall.Kill(server, 0) != nil })
	l.run("up")
	ping = l.say("reviewer", "--to", "@writer", "--expect-reply", "Reply to this message with exactly PONG-2.")
	reply = l.waitMessage("writer", ping.At, "PONG-2", 3*time.Minute)
	t.Logf("measured: after the server restart, answered %s after posting", reply.At.Sub(ping.At))
}

// A session fills one seat at a time. A Claude Code session joins one board, then joins
// another: a message on the new board wakes it and it replies there, while a message to
// its old agent wakes nothing and waits unread for whichever session resumes that agent.
func TestSessionMovesBetweenBoards(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	type paired struct {
		Board struct {
			Name string `json:"name"`
		} `json:"board"`
		Join struct {
			Line string `json:"line"`
		} `json:"join"`
	}
	var first, second paired
	l.decode(l.human, &first, "pair", "writer-reviewer")
	session := l.startClaude("mover", l.project("project", "claude-code"))
	session.submit(first.Join.Line)
	l.waitMessage("claude", time.Time{}, "", 3*time.Minute) // its hello on the first board
	l.waitQuiet(3*time.Minute, "writer", session)

	// The person's terminal now defaults to the second board, so writer and claude there
	// need no --board.
	moving := time.Now()
	l.decode(l.human, &second, "pair", "writer-reviewer", "--new")
	session.submit(second.Join.Line)
	l.waitMessage("claude", moving, "", 3*time.Minute) // its hello on the second board
	l.waitQuiet(3*time.Minute, "writer", session)

	old := l.say("writer", "--board", first.Board.Name, "--to", "@claude", "Reply to this message with exactly OLD-SEAT.")
	l.neverWithin(20*time.Second, "a bundle was handed for the old seat", func() bool { return len(l.handedAfter(old.At)) > 0 })

	ping := l.say("writer", "--to", "@claude", "--expect-reply", "Reply to this message with exactly MOVED-1.")
	wake := l.waitHanded(ping.At)
	reply := l.waitMessage("claude", ping.At, "MOVED-1", 2*time.Minute)
	t.Logf("measured: handed %s after posting, reply on the board %s after posting",
		wake.Time.Sub(ping.At), reply.At.Sub(ping.At))
	checkWake(t, ping, wake)

	var inbox struct {
		Messages []message `json:"messages"`
	}
	l.decode(l.human, &inbox, "inbox", "--peek", "--as", "claude", "--board", first.Board.Name)
	if !slices.ContainsFunc(inbox.Messages, func(m message) bool { return m.Seq == old.Seq }) {
		t.Errorf("message #%d to the old seat isn't waiting unread for it: %+v", old.Seq, inbox.Messages)
	}
	var page struct {
		Messages []message `json:"messages"`
	}
	l.decode(l.human, &page, "read", "--as", "writer", "--board", first.Board.Name, "--limit", "200")
	if slices.ContainsFunc(page.Messages, func(m message) bool { return m.From.Name == "claude" && strings.Contains(m.Body, "OLD-SEAT") }) {
		t.Error("the moved session answered the old seat's message")
	}
}
