//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

// Joining another board retains the session's first seat. Replies and server
// acknowledgements must use the selected board even when both seats have one name.
func TestSessionKeepsBothBoards(t *testing.T) {
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
	l.waitMessage("claude", time.Time{}, "", 3*time.Minute)
	l.waitQuiet(3*time.Minute, "writer", session)

	l.decode(l.human, &second, "pair", "writer-reviewer", "--new")
	session.submit("Run `" + second.Join.Line + "` now. Keep both seats. Whenever an Aboard message arrives, run exactly its requested command using its named board, then stop. Reply only OK now.")
	session.waitIdle(3 * time.Minute)
	l.waitFor(30*time.Second, "the session's second seat", func() bool {
		return slices.Contains(l.agents(second.Board.Name), "claude")
	})

	raw := readFile(t, filepath.Join(l.configDir(), "credentials.json"))
	var saved struct {
		Agents []multiSeatCredential `json:"agents"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	boards := []string{first.Board.Name, second.Board.Name}
	seats := make([]multiSeatCredential, len(boards))
	for i, board := range boards {
		count := 0
		for _, seat := range saved.Agents {
			if seat.Board == board && seat.Name == "claude" {
				seats[i] = seat
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s has %d claude credentials; want one", board, count)
		}
	}
	if seats[0].MemberID == "" || seats[0].MemberID == seats[1].MemberID {
		t.Fatal("the two boards did not retain distinct seats")
	}
	messages := func(board string) []message {
		var page struct {
			Messages []message `json:"messages"`
		}
		l.decode(l.human, &page, "read", "--as", "writer", "--board", board, "--limit", "200")
		return page.Messages
	}
	markers := []string{"OLD-SEAT", "SECOND-SEAT"}
	pings := make([]message, len(boards))
	for i, board := range boards {
		var state struct {
			HeadSeq int `json:"head_seq"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board, nil, &state)
		body := fmt.Sprintf("Run exactly `aboard say --board %s --reply %d %q`. Run no other Aboard command.", board, state.HeadSeq+1, markers[i])
		pings[i] = l.say("writer", "--board", board, "--to", "@claude", "--expect-reply", body)
	}
	for i, board := range boards {
		l.waitFor(3*time.Minute, "the retained seat to reply on "+board, func() bool {
			for _, m := range messages(board) {
				if m.From.Name == "claude" && m.Body == markers[i] && m.ReplyToSeq != nil && *m.ReplyToSeq == pings[i].Seq {
					return true
				}
			}
			return false
		})
	}
	session.waitIdle(2 * time.Minute)
	for i, board := range boards {
		count := 0
		for _, m := range messages(board) {
			if m.Body == markers[1-i] {
				t.Errorf("the other seat's reply appeared on %s", board)
			}
			if m.Body == markers[i] {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s has %d replies; want one", board, count)
		}
		l.waitFor(30*time.Second, "the retained seat's independent acknowledgement on "+board, func() bool {
			var inbox struct {
				Cursor   int       `json:"cursor"`
				Messages []message `json:"messages"`
			}
			l.multiSeatRequest(seats[i].Token, http.MethodGet, "/v1/me/inbox", nil, &inbox)
			return inbox.Cursor >= pings[i].Seq && len(inbox.Messages) == 0
		})
	}
}
