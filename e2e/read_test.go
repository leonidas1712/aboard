//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// sayAs posts a message as agent and returns its sequence number.
func (e *env) sayAs(agent string, args ...string) int {
	e.t.Helper()
	r := e.run(append([]string{"say", "--as", agent, "--json"}, args...)...)
	return int(field(e.t, r.json(e.t), "message.seq").(float64))
}

// asAgent runs a command with ABOARD_AGENT set, as a session that knows its agent would,
// so the hint lines it prints carry no --as.
func (e *env) asAgent(agent string, args ...string) result {
	e.t.Helper()
	r := e.exec([]string{"ABOARD_AGENT=" + agent}, "", args...)
	if r.code != 0 {
		e.t.Fatalf("command failed:\n%s\nserver log:\n%s", r, e.serverLog())
	}
	return r
}

// threeAgents pairs a writer and a reviewer, and joins a second reviewer named critic.
func threeAgents(t *testing.T, e *env) {
	t.Helper()
	line := field(t, e.run("pair", "--json").json(t), "join.line").(string)
	e.run("join", line)
	e.run("join", line, "--name", "critic")
}

// entry is one message as the reviewer reads it: its own, or from another of alex's
// agents.
func entry(seq int, from, role, to, body string) []string {
	sender := "owner_agent"
	if from == "reviewer" {
		sender = "self"
	}
	return []string{fmt.Sprintf("#%d  @%s (%s, %s) → %s", seq, from, role, sender, to), "    " + body}
}

func lines(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		}
	}
	return out
}

// TestReadFiltersAndPagesWithHints reads one board through each filter and window flag,
// and follows the Earlier and Later hint lines.
func TestReadFiltersAndPagesWithHints(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	s1 := e.sayAs("writer", "--to", "@reviewer", "w1")
	s2 := e.sayAs("reviewer", "r1")
	s3 := e.sayAs("writer", "w2")
	s4 := e.sayAs("writer", "--to", "role:reviewer", "w3")
	s5 := e.sayAs("critic", "--to", "@writer", "c1")

	w1 := entry(s1, "writer", "writer", "@reviewer", "w1")
	r1 := entry(s2, "reviewer", "reviewer", "all", "r1")
	w2 := entry(s3, "writer", "writer", "all", "w2")
	w3 := entry(s4, "writer", "writer", "role:reviewer", "w3")
	c1 := entry(s5, "critic", "reviewer", "@writer", "c1")

	// The newest matching messages, and the command for the ones before them.
	expectLines(t, e.asAgent("reviewer", "read", "--from", "@writer", "--limit", "2"), lines(
		"writer-reviewer · 2 messages", w2, w3,
		fmt.Sprintf("Earlier: aboard read --before %d --from @writer --limit 2", s3))...)
	expectLines(t, e.asAgent("reviewer", "read", "--before", fmt.Sprint(s3), "--from", "writer", "--limit", "2"), lines(
		"writer-reviewer · 1 message", w1,
		fmt.Sprintf("Later: aboard read --after %d --from @writer --limit 2", s1))...)

	// critic has the reviewer role too.
	expectLines(t, e.asAgent("reviewer", "read", "--role", "reviewer"), lines("writer-reviewer · 2 messages", r1, c1)...)
	// Addressed to the reviewer by name, by role and to all; never its own.
	expectLines(t, e.asAgent("reviewer", "read", "--to-me"), lines("writer-reviewer · 3 messages", w1, w2, w3)...)

	// --after shows the oldest after it; both directions have more.
	expectLines(t, e.asAgent("reviewer", "read", "--after", fmt.Sprint(s2), "--limit", "1"), lines(
		"writer-reviewer · 1 message", w2,
		fmt.Sprintf("Earlier: aboard read --before %d --limit 1", s3),
		fmt.Sprintf("Later: aboard read --after %d --limit 1", s3))...)

	// --around puts half the limit before the message and the rest from it on.
	expectLines(t, e.asAgent("reviewer", "read", "--around", fmt.Sprint(s3), "--limit", "2"), lines(
		"writer-reviewer · 2 messages", r1, w2,
		fmt.Sprintf("Earlier: aboard read --before %d --limit 2", s2),
		fmt.Sprintf("Later: aboard read --after %d --limit 2", s3))...)

	// An explicit --as is kept in the hint, so the command can be run as shown.
	expectLines(t, e.run("read", "--as", "reviewer", "--to-me", "--limit", "1"), lines(
		"writer-reviewer · 1 message", w3,
		fmt.Sprintf("Earlier: aboard read --before %d --to-me --limit 1 --as reviewer", s4))...)

	expectLines(t, e.asAgent("reviewer", "read", "--from", "@critic", "--after", fmt.Sprint(s5)), "writer-reviewer · no messages")

	page := e.asAgent("reviewer", "read", "--from", "@writer", "--limit", "2", "--json").json(t)
	if got := field(t, page, "prev_before"); got != float64(s3) {
		t.Fatalf("prev_before = %v, want %d", got, s3)
	}
	if got := field(t, page, "next_after"); got != nil {
		t.Fatalf("next_after = %v, want null", got)
	}

	r := e.exec([]string{"ABOARD_AGENT=reviewer"}, "", "read", "--from", "@nobody", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "member_not_found" {
		t.Fatalf("unknown sender: want exit 1 with member_not_found\n%s", r)
	}
	r = e.exec([]string{"ABOARD_AGENT=reviewer"}, "", "read", "--after", "1", "--before", "3")
	if r.code != 2 {
		t.Fatalf("two window flags: want a usage error (exit 2)\n%s", r)
	}
}

// TestReadMarkdownTranscript prints the quickstart conversation as the Markdown transcript
// spec/cli.yaml shows.
func TestReadMarkdownTranscript(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("join", field(t, e.run("pair", "--json").json(t), "join.line").(string))
	e.run("say", "--as", "writer", "--to", "@reviewer", "Draft is in notes.md. Please review it.")
	e.run("say", "--as", "reviewer", "--reply", "6", "Reviewed. Approved.\n\nShip it.")

	expectLines(t, e.run("read", "--as", "writer", "--markdown"),
		"# writer-reviewer · #6–#7",
		"",
		"**#6 @writer** (writer, self) → @reviewer",
		"",
		"> Draft is in notes.md. Please review it.",
		"",
		"**#7 @reviewer** (reviewer, owner_agent) → all · reply to #6",
		"",
		"> Reviewed. Approved.",
		">",
		"> Ship it.",
	)
	if r := e.runExit("read", "--as", "writer", "--markdown", "--json"); r.code != 2 {
		t.Fatalf("--markdown with --json: want a usage error (exit 2)\n%s", r)
	}
}

// TestReadUnderAddressedVisibilityDoesNotLeak checks that filters never show an agent a
// message it may not see.
func TestReadUnderAddressedVisibilityDoesNotLeak(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	e.run("board", "policy", "recommended")
	e.run("say", "--as", "writer", "--to", "@reviewer", "only for the reviewer")
	s := e.sayAs("writer", "--to", "@critic", "only for the critic")

	want := lines("writer-reviewer · 1 message", entry(s, "writer", "writer", "@critic", "only for the critic"))
	expectLines(t, e.run("read", "--as", "critic"), want...)
	expectLines(t, e.run("read", "--as", "critic", "--from", "@writer"), want...)
	expectLines(t, e.run("read", "--as", "critic", "--to-me"), want...)
	expectLines(t, e.run("read", "--as", "critic", "--before", fmt.Sprint(s)), "writer-reviewer · no messages")
	if strings.Contains(e.run("read", "--as", "critic", "--markdown").stdout, "only for the reviewer") {
		t.Fatal("the transcript shows a message addressed to someone else")
	}
}

// watcher is a running aboard watch whose output the test reads line by line.
type watcher struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout chan string
	stderr chan string
	done   chan error
}

func (e *env) watch(args ...string) *watcher {
	e.t.Helper()
	cmd := exec.Command(binary, append([]string{"watch"}, args...)...)
	cmd.Dir = e.dir
	cmd.Env = append([]string{}, e.vars...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	errOut, err := cmd.StderrPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	w := &watcher{t: e.t, cmd: cmd, stdout: make(chan string, 100), stderr: make(chan string, 100), done: make(chan error, 1)}
	scan := func(r io.Reader, to chan string) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			to <- sc.Text()
		}
		close(to)
	}
	go scan(out, w.stdout)
	go scan(errOut, w.stderr)
	e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	return w
}

// expect reads lines from ch until it has read want, in order, failing on any other
// line or when the deadline passes.
func (w *watcher) expect(ch chan string, want ...string) {
	w.t.Helper()
	deadline := time.After(20 * time.Second)
	for _, line := range want {
		select {
		case got, ok := <-ch:
			if !ok {
				w.t.Fatalf("watch output ended; want %q", line)
			}
			if got != line {
				w.t.Fatalf("watch printed %q, want %q", got, line)
			}
		case <-deadline:
			w.t.Fatalf("watch didn't print %q in time", line)
		}
	}
}

// waitFor reads lines from ch until one has prefix, skipping the others.
func (w *watcher) waitFor(ch chan string, prefix string) {
	w.t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				w.t.Fatalf("watch output ended; want a line starting %q", prefix)
			}
			if strings.HasPrefix(got, prefix) {
				return
			}
		case <-deadline:
			w.t.Fatalf("watch didn't print a line starting %q in time", prefix)
		}
	}
}

// stop interrupts watch as Ctrl-C would and checks that it exits 0 with nothing more
// on stdout.
func (w *watcher) stop() {
	w.t.Helper()
	if err := w.cmd.Process.Signal(syscall.SIGINT); err != nil {
		w.t.Fatal(err)
	}
	var rest []string
	for line := range w.stdout {
		rest = append(rest, line)
	}
	if err := w.cmd.Wait(); err != nil {
		w.t.Fatalf("watch didn't exit 0 after Ctrl-C: %v", err)
	}
	if len(rest) > 0 {
		w.t.Fatalf("watch printed more than expected: %q", rest)
	}
}

// TestWatchFollowsTheBoardLive starts aboard watch, posts while it runs, restarts the
// server under it, and checks every message is printed once, in order.
func TestWatchFollowsTheBoardLive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("join", field(t, e.run("pair", "--json").json(t), "join.line").(string))
	e.run("say", "--as", "writer", "--to", "@reviewer", "first")

	w := e.watch()
	w.expect(w.stdout, "#6  @writer (writer, owner_agent) → @reviewer", "    first")
	w.expect(w.stderr, "Watching writer-reviewer. Stop with Ctrl-C.")

	e.run("say", "--as", "reviewer", "second")
	w.expect(w.stdout, "#7  @reviewer (reviewer, owner_agent) → all", "    second")

	// The stream drops when the server stops. Once it is back, watch reconnects and
	// prints what it missed.
	e.run("down")
	w.waitFor(w.stderr, "Lost the connection")
	e.run("up")
	e.run("say", "--as", "writer", "third")
	w.expect(w.stdout, "#8  @writer (writer, owner_agent) → all", "    third")
	w.stop()
}

// TestWatchJSONLinesWithAFilter follows one sender's messages as JSON Lines.
func TestWatchJSONLinesWithAFilter(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("join", field(t, e.run("pair", "--json").json(t), "join.line").(string))
	e.run("say", "--as", "reviewer", "old one")
	e.run("say", "--as", "reviewer", "old two")

	w := e.watch("--from", "@reviewer", "--limit", "1", "--json")
	body := func() string {
		t.Helper()
		select {
		case line := <-w.stdout:
			var v struct {
				Board   string `json:"board"`
				Message struct {
					Body string `json:"body"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &v); err != nil || v.Board != "writer-reviewer" {
				t.Fatalf("not a WatchOutput line (%v): %s", err, line)
			}
			return v.Message.Body
		case <-time.After(20 * time.Second):
			t.Fatal("watch printed nothing in time")
			return ""
		}
	}
	if got := body(); got != "old two" {
		t.Fatalf("first line is %q, want the newest matching message", got)
	}
	w.expect(w.stderr, "Watching writer-reviewer. Stop with Ctrl-C.")
	e.run("say", "--as", "writer", "not from the reviewer")
	e.run("say", "--as", "reviewer", "new")
	if got := body(); got != "new" {
		t.Fatalf("got %q, want the reviewer's new message", got)
	}
	w.stop()
}
