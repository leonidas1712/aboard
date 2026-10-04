//go:build live

package live

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// With delivery mode humans, a peer's message leaves an idle Claude Code session
// asleep; its owner's message wakes it, and that bundle carries the peer's message too.
func TestHumansModeWakesOnlyForPeople(t *testing.T) {
	only(t, "claude-code")
	requireClaude(t)
	parallel(t)
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
	checkWake(t, l.driverFor("claude"), own, wake)
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
	only(t, "claude-code")
	requireClaude(t)
	parallel(t)
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

// A session fills one seat at a time. A Claude Code session joins one board, then joins
// another: a message on the new board wakes it and it replies there, while a message to
// its old agent wakes nothing and waits unread for whichever session resumes that agent.
func TestSessionMovesBetweenBoards(t *testing.T) {
	only(t, "claude-code")
	requireClaude(t)
	parallel(t)
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
	checkWake(t, l.driverFor("claude"), ping, wake)

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
