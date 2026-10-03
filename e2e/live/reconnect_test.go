//go:build live

package live

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A Claude Code session that exits and is resumed with claude --resume <id> in the same
// pane keeps its session id, so its hooks bind it again to the agent it filled: the
// message sent while it was closed is delivered when the resumed session's first turn
// ends, and answered, with no aboard resume.
func TestResumedClaudeSessionReconnects(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	writer := l.startClaude("writer", l.project("project", "claude-code"))
	writer.bind("writer")
	l.provesReconnect(writer, "claude-code", "writer", "reviewer", writer.quit, func(id string) {
		writer.respawn(l.vars, claudeArgv("--resume", id))
		writer.waitClaudeReady()
	})
}

// A Codex session resumed with codex resume <id> keeps its thread id, so it reconnects
// the same way. Codex runs its threads and their hooks in an app server of its own that
// outlives the terminal: quitting Codex leaves the thread loaded there, and messages
// still reach it. The session closes when that app server stops (as when the machine
// restarts), so the test stops it. A resumed Codex runs its session-start hook only once
// its first turn starts (Codex 0.159.3).
func TestResumedCodexSessionReconnects(t *testing.T) {
	requireCodex(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	reviewer := l.startCodex("reviewer", l.project("project", "codex"))
	reviewer.bind("reviewer")
	closeCodex := func() {
		reviewer.quit()
		gone := waitQuietly(10*time.Second, func() bool { return l.presence("reviewer") == "no_session" })
		t.Logf("measured: within 10 s of quitting Codex, reviewer disconnected: %v", gone)
		l.stopProcesses(filepath.Join(l.dir, "codex-home"))
	}
	l.provesReconnect(reviewer, "codex", "reviewer", "writer", closeCodex, func(id string) {
		reviewer.respawn(l.vars, l.codexArgv("resume", id))
		reviewer.waitCodexReady()
	})
}

// startsAfter lists the session starts at or after t.
func (l *lab) startsAfter(t time.Time) []sessionStart {
	var out []sessionStart
	for _, s := range l.starts() {
		if !s.Time.Before(t) {
			out = append(out, s)
		}
	}
	return out
}

// provesReconnect closes the session in p, which fills agent, posts a message to agent
// from sender while it is closed, resumes the same session with resume, and checks that
// the session came back with the same id, as agent, and answered. A person coming back
// to a session types something; the waiting message arrives when that turn ends, since
// neither harness takes a bundle before a turn has run (Claude Code's stop hook waits
// only after a turn, and Codex runs no hook until one starts).
func (l *lab) provesReconnect(p *pane, harness, agent, sender string, closeSession func(), resume func(id string)) {
	l.t.Helper()
	var id string
	for _, s := range l.starts() {
		if after, ok := strings.CutPrefix(s.Session, harness+":"); ok {
			id = after
		}
	}
	if id == "" {
		l.t.Fatalf("the daemon's log has no %s session start", harness)
	}
	l.waitPresence(agent, "idle", 30*time.Second)
	closeSession()
	l.waitPresence(agent, "no_session", 15*time.Second)

	ping := l.say(sender, "--to", "@"+agent, "--expect-reply", "Reply to this message with exactly RECONNECT-1.")
	l.neverWithin(5*time.Second, "a bundle was handed while the session was closed", func() bool { return len(l.handedAfter(ping.At)) > 0 })

	resumed := time.Now()
	resume(id)
	p.submit("Reply only OK.")
	var back sessionStart
	l.waitFor(60*time.Second, "the resumed session's start hook", func() bool {
		if s := l.startsAfter(resumed); len(s) > 0 {
			back = s[0]
			return true
		}
		return false
	})
	l.t.Logf("measured: the resumed session started as %s (source %q, reopened %v, %d agent)", back.Session, back.Source, back.Reopened, back.Agents)
	if back.Session != harness+":"+id {
		l.t.Fatalf("the resumed session has the id %s; want the same as before, %s:%s", back.Session, harness, id)
	}
	if !back.Reopened || back.Agents != 1 {
		l.t.Fatalf("the resumed session wasn't bound again to %s: %+v", agent, back)
	}
	reply := l.waitMessage(agent, ping.At, "RECONNECT-1", 3*time.Minute)
	l.t.Logf("measured: answered %s after the session was resumed", reply.At.Sub(resumed))
}
