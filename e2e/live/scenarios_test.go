//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// The scenarios here are the live kit: each runs once for every harness with a driver
// (or for HARNESS=<name>), with what differs between harnesses in their drivers and
// profiles. Each records its result for the README's support matrix.

// wakeBound is how soon an idle session must be handed a message once it is posted.
const wakeBound = 2 * time.Second

// queueGather is how long the daemon gathers messages before handing them to a harness
// whose own queue takes them (spec/delivery.md, Codex).
const queueGather = 2 * time.Second

// pongBound is how soon a reply must reach a session that asked for it.
const pongBound = 30 * time.Second

// checkWake fails the test if the session was handed the message later than the bound
// for its harness.
func checkWake(t *testing.T, d *driver, posted message, wake handover) {
	t.Helper()
	bound := wakeBound
	if !d.waitsForIdle() {
		bound += queueGather
	}
	if dur := wake.Time.Sub(posted.At); dur > bound {
		t.Errorf("the idle session was handed message #%d %s after it was posted; want within %s", posted.Seq, dur, bound)
	}
}

// slowFor is how long a scenario's slow task takes for a harness: base plus the
// harness's place among the drivers, so the scenario running for two harnesses at once
// never takes the other's sleep for its own.
func slowFor(t *testing.T, d *driver, base int) int {
	t.Helper()
	return base + slices.Index(drivers(t), d)
}

// TestEveryHarnessHasALiveDriver checks the live kit can run every harness with a
// profile. It starts no harness.
func TestEveryHarnessHasALiveDriver(t *testing.T) {
	profiles, err := support.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if newDriver(p) == nil {
			t.Errorf("%s has a profile but no live driver: add one to newDriver in drivers_test.go", p.Harness)
		}
		if len(p.Interactive.Resume) == 0 && p.Lifecycle.ResumeKeepsID {
			t.Errorf("%s keeps its session id on resume, but its profile has no interactive.resume", p.Harness)
		}
	}
}

// boardOf reads the board's name from a join line.
var boardOf = regexp.MustCompile(`Join Aboard board ([a-z0-9-]+) on`)

// agents lists a board's agents, in the order they joined, read with the person's login.
func (l *lab) agents(board string) []string {
	l.t.Helper()
	token, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(l.t.Context(), http.MethodGet, "http://"+l.addr+"/v1/boards/"+board+"/members", http.NoBody)
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Members []struct {
			Name     string    `json:"name"`
			Kind     string    `json:"kind"`
			JoinedAt time.Time `json:"joined_at"`
		} `json:"members"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	sort.SliceStable(out.Members, func(i, j int) bool { return out.Members[i].JoinedAt.Before(out.Members[j].JoinedAt) })
	var names []string
	for _, m := range out.Members {
		if m.Kind == "agent" {
			names = append(names, m.Name)
		}
	}
	return names
}

// Two sessions of the harness pair the way the quickstart shows: the first is asked in
// plain words, the second gets the join line it gives, joins and says hello, which
// proves the baseline. Then a message to the first, sitting idle, wakes it, its presence
// goes working and back to idle, and it replies on the board with no one typing.
// Was TestIdleClaudeWakesAndReplies and TestIdleCodexWakesAndReplies.
func TestWakesAndReplies(t *testing.T) {
	eachHarness(t, "WakesAndReplies", func(t *testing.T, d *driver, rec *recorder) {
		rec.expect("JoinsAndTalks")
		l := newLab(t)
		d.setUp(l)
		first := d.start(l, "first", l.project("first-project", d.p.Harness))
		second := d.start(l, "second", l.project("second-project", d.p.Harness))

		first.submit("Pair with another agent on Aboard.")
		var line string
		l.waitFor(3*time.Minute, "the first session to give a join line", func() bool {
			line = joinLine.FindString(first.screen())
			return line != ""
		})
		first.waitIdle(2 * time.Minute)
		board := boardOf.FindStringSubmatch(line)[1]
		second.submit(line)
		var names []string
		l.waitFor(3*time.Minute, "the second session's agent to join", func() bool {
			names = l.agents(board)
			return len(names) >= 2
		})
		a, b := names[0], names[1]
		l.waitMessage(b, time.Time{}, "", 3*time.Minute) // its hello
		l.waitQuiet(4*time.Minute, b, first, second)
		rec.milestone("JoinsAndTalks")
		l.waitPresence(a, "idle", 30*time.Second)

		seen := l.watchPresence(a)
		ping := l.say(b, "--to", "@"+a, "--expect-reply", "Reply to this message with exactly PONG-1.")
		wake := l.waitHanded(ping.At)
		reply := l.waitMessage(a, ping.At, "PONG-1", 3*time.Minute)
		first.waitIdle(2 * time.Minute)
		l.waitPresence(a, "idle", 30*time.Second)
		if states := seen(); !slices.Contains(states, "working") {
			t.Errorf("%s's presence during its reply turn went %v; want working on the way", a, states)
		}
		t.Logf("measured: handed %s after posting, reply on the board %s after posting",
			wake.Time.Sub(ping.At), reply.At.Sub(ping.At))
		checkWake(t, d, ping, wake)
		if reply.ReplyToSeq == nil || *reply.ReplyToSeq != ping.Seq {
			t.Errorf("%s's PONG-1 (#%d) isn't a reply to #%d", a, reply.Seq, ping.Seq)
		}
	})
}

// wiringCheck asks a session to run the skill's wiring check up to PING 3.
const wiringCheck = "Run the Aboard wiring check with @reviewer now, going up to PING 3 instead of PING 2."

// After one prompt, two sessions of the harness run the skill's wiring check to PING 3:
// six messages go back and forth with no one typing, and then they stop.
// Was TestClaudeExchangesFiveMessages.
func TestPingPong(t *testing.T) {
	eachHarness(t, "PingPong", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("writer-project", d.p.Harness))
		reviewer := d.start(l, "reviewer", l.project("reviewer-project", d.p.Harness))
		writer.bind("writer")
		reviewer.bind("reviewer")

		start := time.Now()
		writer.submit(wiringCheck)
		l.waitMessage("reviewer", start, "PONG 3", 6*time.Minute)
		l.waitQuiet(3*time.Minute, "reviewer", writer, reviewer)
		exchangedAlone(t, l.messages("reviewer"), start, 5, 8)
		if l.typed != 3 {
			t.Errorf("%d prompts were typed; want the two first prompts and the one asking for the check", l.typed)
		}
	})
}

// After one prompt to a session of one harness, it and a session of another run the
// wiring check to PING 3 with no one typing. Each pair of harnesses runs once.
// Was TestClaudeAndCodexExchange.
func TestPingPongAcrossHarnesses(t *testing.T) {
	all := drivers(t)
	for i, a := range all {
		for _, b := range all[i+1:] {
			if !selected(a.p.Harness) && !selected(b.p.Harness) {
				continue
			}
			t.Run(a.p.Harness+"-with-"+b.p.Harness, func(t *testing.T) {
				a.require(t)
				b.require(t)
				t.Parallel()
				record(t, a, "PingPongAcrossHarnesses")
				record(t, b, "PingPongAcrossHarnesses")
				l := newLab(t)
				// Every process in the lab, the hooks and the daemon they start included,
				// needs what each harness's set-up gives it before anything starts.
				a.setUp(l)
				b.setUp(l)
				l.pairCLI()
				writer := a.start(l, "writer", l.project("writer-project", a.p.Harness))
				reviewer := b.start(l, "reviewer", l.project("reviewer-project", b.p.Harness))
				writer.bind("writer")
				reviewer.bind("reviewer")

				start := time.Now()
				writer.submit(wiringCheck)
				l.waitMessage("reviewer", start, "PONG 3", 6*time.Minute)
				l.waitQuiet(3*time.Minute, "reviewer", writer, reviewer)
				exchangedAlone(t, l.messages("reviewer"), start, 5, 8)
			})
		}
	}
}

// exchangedAlone checks the messages after start go back and forth between two agents,
// at least least of them and at most most, so the exchange ended rather than looping.
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

// The session starts the wiring check itself, with a person's agent on the other side.
// Each PONG must reach the session promptly, measured from the daemon's log, not only
// be posted: the session either waits for it with aboard say --wait-reply or ends its
// turn so delivery can bring it, and never keeps its turn busy polling.
// Was TestCodexStartsPingPong.
func TestRepliesReachPromptly(t *testing.T) {
	eachHarness(t, "RepliesReachPromptly", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		reviewer := d.start(l, "reviewer", l.project("project", d.p.Harness))
		reviewer.bind("reviewer")

		start := time.Now()
		reviewer.submit("Run the Aboard wiring check with @writer now.")
		for n := 1; n <= 2; n++ {
			ping := l.waitMessage("reviewer", start, fmt.Sprintf("PING %d", n), 4*time.Minute)
			pong := l.say("writer", "--reply", strconv.Itoa(ping.Seq), fmt.Sprintf("PONG %d", n))
			var at time.Time
			var how string
			l.waitFor(2*time.Minute, fmt.Sprintf("PONG %d to reach the session", n), func() bool {
				var ok bool
				at, how, ok = l.reached(pong.Seq)
				return ok
			})
			t.Logf("measured: PONG %d reached the session %s after it was posted (%s)", n, at.Sub(pong.At), how)
			if dur := at.Sub(pong.At); dur > pongBound {
				t.Errorf("PONG %d reached the session %s after it was posted; want within %s. A reply sat waiting while the session kept its turn busy", n, dur, pongBound)
			}
		}
		l.waitFor(time.Minute, "PONG 2 to be acknowledged", func() bool { return len(l.inbox("reviewer")) == 0 })
		reviewer.waitIdle(2 * time.Minute)
	})
}

// While a turn runs a slow task twice, a message from the agent's owner (posted with the
// owner login on the API) reaches it at the next tool boundary and is acted on in that
// turn. Two peer messages sent at the same time, one urgent, never enter the turn: a
// harness whose hook waits for idle is handed them in one bundle once the turn ends, and
// one with its own queue holds them there until it does.
// Was TestOwnerReachesBusyClaude and TestOwnerReachesBusyCodex.
func TestOwnerReachesBusy(t *testing.T) {
	eachHarness(t, "OwnerReachesBusy", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Has("tool-boundary") {
			rec.notApplicable("the harness has no tool boundary hook: the owner's messages wait for the turn's end")
		}
		l := newLab(t)
		d.setUp(l)
		board := l.pairCLI()
		proj := l.project("project", d.p.Harness)
		secs := slowFor(t, d, 20)
		writeSlowTask(t, proj, secs)
		writer := d.start(l, "writer", proj)
		writer.bind("writer")

		writer.submit("Run `./" + slowTask + "` in its own command, in the foreground, and wait for it. When it has finished, run `./" +
			slowTask + "` again the same way and wait for it. Then run: aboard say \"DONE\".")
		l.waitFor(2*time.Minute, "the session to run the slow task", func() bool { return pgrep(fmt.Sprintf("sleep %d", secs)) })
		sent := time.Now()
		own := l.postAsOwner(board, "@writer", `OWNER: run aboard say "OWNER-ACK" right away, then carry on with your task.`)
		peer1 := l.say("reviewer", "--to", "@writer", "--urgent", "PEER 1 of 2 (urgent): no action needed.")
		peer2 := l.say("reviewer", "--to", "@writer", `PEER 2 of 2: run aboard say "PEER-ACK".`)

		ownerAck := l.waitMessage("writer", sent, "OWNER-ACK", 3*time.Minute)
		done := l.waitMessage("writer", sent, "DONE", 4*time.Minute)
		peerAck := l.waitMessage("writer", sent, "PEER-ACK", 4*time.Minute)
		writer.waitIdle(2 * time.Minute)
		at, how, ok := l.reached(own.Seq)
		t.Logf("measured: the owner's message reached the turn %s after posting (%s); OWNER-ACK %s, DONE %s, PEER-ACK %s after sending",
			at.Sub(own.At), how, ownerAck.At.Sub(sent), done.At.Sub(sent), peerAck.At.Sub(sent))
		if !ok || how != "tool boundary" {
			t.Fatalf("the owner's message reached the session by %q; want at a tool boundary of the busy turn", how)
		}
		if !ownerAck.At.Before(done.At) {
			t.Errorf("OWNER-ACK came after DONE: the owner's message wasn't acted on in the busy turn")
		}
		for _, h := range l.logged("bundle handed") {
			if slices.Contains(h.Seqs, own.Seq) {
				t.Errorf("the owner's message was also handed in a bundle at %s", h.Time)
			}
		}
		for _, h := range l.logged("tool boundary") {
			if slices.Contains(h.Seqs, peer1.Seq) || slices.Contains(h.Seqs, peer2.Seq) {
				t.Errorf("a peer's message was added to the busy turn at %s", h.Time)
			}
		}
		if d.waitsForIdle() {
			// The agent may also act on the waiting notice and fetch the peers' messages
			// itself; then nothing is handed. Otherwise one bundle, after the turn ended.
			var bundles int
			for _, h := range l.handedAfter(sent) {
				if h.Error == "" && (slices.Contains(h.Seqs, peer1.Seq) || slices.Contains(h.Seqs, peer2.Seq)) {
					bundles++
					if h.Time.Before(done.At) {
						t.Errorf("the peers' messages were handed at %s, before the turn ended (DONE at %s)", h.Time, done.At)
					}
				}
			}
			if bundles > 1 {
				t.Errorf("the peers' messages came in %d bundles; want one", bundles)
			}
		} else if peerAck.At.Before(done.At) {
			t.Errorf("PEER-ACK came before DONE: the harness's queue let a peer's message into the busy turn")
		}
		l.waitFor(30*time.Second, "every message to be acknowledged", func() bool { return len(l.writerInbox()) == 0 })
	})
}

// A peer's message to a busy session never enters the turn: at the next tool boundary
// only a notice names it, once, and the message itself arrives when the turn ends.
func TestPeerWaitsButNoticeArrives(t *testing.T) {
	eachHarness(t, "PeerWaitsButNoticeArrives", func(t *testing.T, d *driver, rec *recorder) {
		switch {
		case !d.p.Delivery.WaitingNotice:
			rec.notApplicable("the profile declares no waiting notice")
		case !d.waitsForIdle():
			rec.notApplicable("the harness's own queue takes a peer's message as it comes, so nothing waits to be named")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		proj := l.project("project", d.p.Harness)
		secs := slowFor(t, d, 25)
		writeSlowTask(t, proj, secs)
		writer := d.start(l, "writer", proj)
		writer.bind("writer")

		writer.submit("Run `./" + slowTask + "` in the foreground, not as a background task, and wait for it. When it has finished, run `./" +
			slowTask + "` again the same way. Then reply DONE.")
		l.waitFor(2*time.Minute, "the session to run the slow task", func() bool { return pgrep(fmt.Sprintf("sleep %d", secs)) })
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
	})
}

// A session killed (SIGKILL) after a wake, before its turn ends, never confirms the
// bundle: the message stays unread, the daemon closes the session within 5 seconds, and
// the next session that resumes the agent receives it and acts on it.
// Was TestKilledSessionRedelivers, for Claude Code.
func TestKilledSessionRedelivers(t *testing.T) {
	eachHarness(t, "KilledSessionRedelivers", func(t *testing.T, d *driver, rec *recorder) {
		switch {
		case !d.waitsForIdle():
			rec.notApplicable("the harness's own queue confirms a bundle when it takes it, so a killed session has none unconfirmed")
		case d.p.Lifecycle.Liveness != "process":
			rec.notApplicable("the session's liveness isn't its harness process")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		proj := l.project("project", d.p.Harness)
		secs := slowFor(t, d, 30)
		writeSlowTask(t, proj, secs)
		first := d.start(l, "first", proj)
		first.bind("writer")
		open := l.openSessions()

		job := l.say("reviewer", "--to", "@writer",
			"Run `./"+slowTask+"` in the foreground, not as a background task, and wait for it. Then run: aboard say \"KILLTEST-DONE\".")
		l.waitFor(2*time.Minute, "the woken session to run the slow task", func() bool { return pgrep(fmt.Sprintf("sleep %d", secs)) })
		if err := syscall.Kill(first.pid(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		killed := time.Now()
		l.waitFor(15*time.Second, "the daemon to close the killed session", func() bool { return l.openSessions() == open-1 })
		t.Logf("measured: the daemon closed the killed session %s after the kill", time.Since(killed))
		// The daemon looks for gone harness processes every 5 seconds; the extra second is
		// this poll and the status command.
		if dur := time.Since(killed); dur > 6*time.Second {
			t.Errorf("the daemon closed the killed session after %s; want within 5 seconds", dur)
		}
		_ = command(t.Context(), "pkill", "-x", "-f", fmt.Sprintf("sleep %d", secs)).Run()
		if !slices.ContainsFunc(l.writerInbox(), func(m message) bool { return m.Seq == job.Seq }) {
			t.Fatalf("message #%d was acknowledged though the session that woke for it was killed before confirming", job.Seq)
		}

		second := d.start(l, "second", proj)
		second.bind("writer")
		l.waitMessage("writer", job.At, "KILLTEST-DONE", 3*time.Minute)
		if n := len(l.handedAfter(job.At)); n < 2 {
			t.Errorf("%d bundles handed after the message; want one to the killed session and one to the next", n)
		}
	})
}

// Stopping the delivery daemon while a session is idle, and separately the local
// server, loses nothing: the daemon starts again (a waiting hook starts it, or the next
// command that needs it), the server comes back with aboard up, and each message sent
// afterwards gets its answer.
// Was TestRestartsLoseNothing, for Claude Code.
func TestRestartsLoseNothing(t *testing.T) {
	eachHarness(t, "RestartsLoseNothing", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")

		oldDaemon := l.daemonPID()
		if err := syscall.Kill(oldDaemon, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if _, waits := d.p.Hook("wait"); !waits {
			// Nothing of the harness's waits on the daemon, so it starts when a command
			// needs it; aboard daemon start is that command, run where the person would.
			l.waitFor(15*time.Second, "the old daemon to stop", func() bool { return syscall.Kill(oldDaemon, 0) != nil })
			l.run("daemon", "start")
		}
		l.waitFor(30*time.Second, "the daemon to start again", func() bool {
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
	})
}

// aboard init --yes --scope project sets up only the project: every install item with a
// project path is there, a session started there runs Aboard's hooks and one started
// elsewhere doesn't, doctor and status name the project's setup, and the person's own
// config is untouched.
// Was TestProjectScopeInit, for Claude Code.
func TestProjectScopeSetup(t *testing.T) {
	eachHarness(t, "ProjectScopeSetup", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		proj := l.project("project", d.p.Harness)
		var hooksFile string
		for _, it := range d.p.Install {
			if it.Project == "" {
				continue
			}
			path := filepath.Join(proj, filepath.FromSlash(it.Project))
			raw := readFile(t, path)
			if it.Kind == "hooks" {
				hooksFile = path
				if !strings.Contains(string(raw), l.bin+" hook "+d.p.Harness+" ") {
					t.Errorf("%s doesn't run %s", path, l.bin)
				}
			}
		}
		if it, ok := d.p.Item("allow-rule"); ok && it.Global == "" && hooksFile != "" {
			var settings struct {
				Permissions struct {
					Allow []string `json:"allow"`
				} `json:"permissions"`
			}
			if err := json.Unmarshal(readFile(t, hooksFile), &settings); err != nil {
				t.Fatalf("%s: %v", hooksFile, err)
			}
			if !slices.Contains(settings.Permissions.Allow, it.Rule) {
				t.Errorf("the project's settings don't allow aboard commands: %v", settings.Permissions.Allow)
			}
		}

		// Doctor and status read the files, so they run before any harness starts: the
		// suite rewrites Codex's hook commands to carry the lab's variables when it starts
		// Codex, which doctor would then report as edited.
		checks := map[string]check{}
		for _, c := range l.doctor(proj) {
			checks[c.Name] = c
		}
		if c := checks[d.p.CheckName+"_hooks"]; c.Level != "ok" || !strings.Contains(c.Message, hooksFile) {
			t.Errorf("doctor in the project should find the hooks in %s: %+v", hooksFile, c)
		}
		if c := checks[d.p.CheckName+"_skill"]; c.Level != "ok" {
			t.Errorf("doctor in the project should find the skill: %+v", c)
		}
		var status struct {
			Setup struct {
				Project []string `json:"project"`
			} `json:"setup"`
		}
		l.decode(proj, &status, "status")
		if !slices.Contains(status.Setup.Project, d.p.Harness) {
			t.Errorf("status in the project: setup.project = %v, want %s", status.Setup.Project, d.p.Harness)
		}

		in := d.start(l, "in-project", proj)
		if d.startsAtFirstTurn {
			in.submit("Reply only OK.")
			in.waitIdle(2 * time.Minute)
		}
		l.waitFor(60*time.Second, "the session-start hook to open a session", func() bool { return l.openSessions() == 1 })

		elsewhere := filepath.Join(l.dir, "elsewhere")
		if err := os.MkdirAll(elsewhere, 0o750); err != nil {
			t.Fatal(err)
		}
		out := d.startPlain(l, "elsewhere", elsewhere)
		if d.startsAtFirstTurn {
			out.submit("Reply only OK.")
			out.waitIdle(2 * time.Minute)
		}
		l.neverWithin(5*time.Second, "a session outside the project ran Aboard's hooks", func() bool { return l.openSessions() > 1 })
		if diff := configDiff(realConfig, configSums()); diff != "" {
			t.Errorf("setting up a project changed the person's own config:\n%s", diff)
		}
	})
}

// A session that quits and is resumed with the same id keeps it, so its hooks bind it
// again to the agent it filled (D157): the message sent while it was closed, which
// wakes nothing, is delivered when the resumed session's first turn ends, and answered,
// with no aboard resume. A harness whose sessions outlive the terminal (Codex's app
// server) has what keeps them stopped as well, as when the machine restarts.
// Was TestResumedClaudeSessionReconnects and TestResumedCodexSessionReconnects.
func TestResumeReconnects(t *testing.T) {
	eachHarness(t, "ResumeReconnects", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Lifecycle.ResumeKeepsID {
			rec.notApplicable("a resumed session has a new id")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")
		closeSession := func() {
			writer.quit()
			if d.p.Lifecycle.OutlivesTerminal {
				gone := waitQuietly(10*time.Second, func() bool { return l.presence("writer") == "no_session" })
				t.Logf("measured: within 10 s of quitting, writer disconnected: %v", gone)
				d.stopBackground(l)
			}
		}
		l.provesReconnect(writer, d.p.Harness, "writer", "reviewer", closeSession, func(id string) {
			writer.respawn(l.vars, d.resumeArgv(l, id))
			d.ready(writer)
		})
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
// no harness takes a bundle before a turn has run (Claude Code's stop hook waits only
// after a turn, and Codex runs no hook until one starts).
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

// A subagent runs aboard in its parent's session, so without a mark it would act as the
// parent. Asked to run aboard status and aboard say, the subagent's commands are marked
// and refused: nothing reaches the board, and the refusal is in the harness's
// transcripts, which shows the subagent did run aboard.
// Was TestClaudeSubagentCannotActAsItsParent.
func TestSubagentCannotActAsItsParent(t *testing.T) {
	eachHarness(t, "SubagentCannotActAsItsParent", func(t *testing.T, d *driver, rec *recorder) {
		if d.p.SubagentIdentity != "marked" && d.p.SubagentIdentity != "seats" {
			rec.notApplicable("subagent_identity none: a subagent may act as its parent, which the docs page states")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		proj := l.project("project", d.p.Harness)
		var report func()
		if d.subagentSetup != nil {
			report = d.subagentSetup(l, proj)
		}
		writer := d.start(l, "writer", proj)
		writer.bind("writer")

		start := time.Now()
		writer.submit(fmt.Sprintf(d.subagentPrompt, "the shell command `aboard status --json`, then `aboard say --to @reviewer 'subagent was here'`"))
		asked := 0
		l.waitFor(6*time.Minute, "the session to report its subagent done", func() bool {
			if d.approve(writer) {
				asked++
			}
			msgs, _, _ := l.tryMessages("writer")
			return slices.ContainsFunc(msgs, func(m message) bool { return m.From.Name == "writer" && strings.Contains(m.Body, "SUBAGENT-DONE") })
		})
		writer.waitIdle(2 * time.Minute)
		if report != nil {
			report()
		}
		t.Logf("the harness asked before a command %d times", asked)

		for _, m := range l.messages("reviewer") {
			if strings.Contains(m.Body, "subagent was here") {
				t.Fatalf("the subagent posted as %s: #%d %q", m.From.Name, m.Seq, m.Body)
			}
		}
		_, hookMarks := d.p.Hook("mark-subagent")
		marked, refused := !hookMarks, false
		for _, path := range d.transcripts(l) {
			raw, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				continue
			}
			text := string(raw)
			marked = marked || strings.Contains(text, "export ABOARD_SUBAGENT=")
			refused = refused || strings.Contains(text, "subagent_without_seat") || strings.Contains(text, "runs in a subagent")
		}
		t.Logf("in the transcripts: command marked %v, refusal %v (took %s)", marked, refused, time.Since(start).Round(time.Second))
		if !marked || !refused {
			t.Fatalf("the subagent's aboard command wasn't marked and refused (marked %v, refused %v)", marked, refused)
		}
	})
}
