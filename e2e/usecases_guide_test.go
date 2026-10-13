//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The use-case pages under docs/use-cases/ show the prompts a person pastes and the
// commands the agents then run. These tests run those commands, in the pages' order,
// with stand-in harness sessions on a fresh machine, and check the output each page
// shows. Prompts to agents can't run; the commands they lead to do.

// writeFile writes a file into the env's project folder, as an agent would.
func (e *env) writeFile(name, body string) {
	e.t.Helper()
	path := filepath.Join(e.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// sentSeq is the message number in a say's "Sent #N …" line.
func sentSeq(t *testing.T, r result) string {
	t.Helper()
	m := regexp.MustCompile(`^(?:Sent|Asked @\S+) #(\d+) `).FindStringSubmatch(r.stdout)
	if m == nil {
		t.Fatalf("no message number in:\n%s", r)
	}
	return m[1]
}

// TestGuideUseCaseWriterAndReviewer runs docs/use-cases/writer-and-reviewer.mdx.
func TestGuideUseCaseWriterAndReviewer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// Pair the two sessions as writer and reviewer.
	writer := e.claudeSession("s-writer")
	pair := writer.run("pair", "writer-reviewer", "--title", "Retry design").lines()
	if pair[1] != "Created board writer-reviewer (Retry design) and joined as claude (writer, owner alex)" ||
		!strings.HasPrefix(pair[len(pair)-1], "Join Aboard board writer-reviewer on localhost:"+e.port()+" as reviewer with code ") {
		t.Fatalf("pair writer-reviewer:\n%s", strings.Join(pair, "\n"))
	}
	reviewer := e.claudeSession("s-reviewer")
	if r := reviewer.run("join", pair[len(pair)-1]); !strings.HasPrefix(r.stdout, "Joined board writer-reviewer as claude-2 (reviewer, owner alex)\n") {
		t.Fatalf("the reviewer's join:\n%s", r)
	}

	// The writer: task, file and review request.
	e.writeFile("docs/retry.md", "# Retry design\n")
	expectLines(t, writer.run("task", "new", "Write the retry design"),
		"WRI-1 is yours on writer-reviewer: Write the retry design",
		"Your messages are about WRI-1 until it's done; add --task to say otherwise.",
		"Working on: Write the retry design")
	expectLines(t, writer.run("file", "put", "docs/retry.md", "--name", "retry.md"), "Put retry.md v1 on writer-reviewer")
	request := writer.run("say", "--to", "role:reviewer", "--file", "retry.md", "--expect-reply", "Ready for review: retry.md")
	expectLines(t, request, "Sent #10 to role:reviewer on writer-reviewer · about WRI-1", "@claude-2 gets it now.")

	// The reviewer: read and reply.
	if r := reviewer.run("inbox"); !strings.Contains(r.stdout, "Ready for review: retry.md") || !strings.Contains(r.stdout, `Attached file "retry.md" v1.`) {
		t.Fatalf("the reviewer's inbox:\n%s", r)
	}
	reviewer.run("file", "get", "retry.md", "retry.md")
	expectLines(t, reviewer.run("say", "--reply", "10", "Two comments: cap the backoff, and say what happens after the last retry."),
		"Sent #11 to @claude on writer-reviewer · about WRI-1", "@claude gets it now.")

	// The writer asks for a decision; the person answers from a terminal.
	asked := writer.run("ask", "Drafted the retry design.\nHow many retries before we give up?", "3", "5")
	expectLines(t, asked,
		"Asked @alex #12 on writer-reviewer · about WRI-1",
		"WRI-1 is Blocked until @alex answers. The answer wakes you: end your turn, or work on something else.",
		"@alex sees it on the board or in their inbox.")
	e.run("say", "--reply", sentSeq(t, asked), "--option", "1")

	// The next draft, the approval and done.
	e.writeFile("docs/retry.md", "# Retry design\n\nThree retries, backoff capped at 30s.\n")
	expectLines(t, writer.run("file", "put", "docs/retry.md", "--name", "retry.md"), "Put retry.md v2 on writer-reviewer")
	again := writer.run("say", "--to", "role:reviewer", "--file", "retry.md", "--expect-reply", "Addressed both comments. Ready for another review.")
	if seq := sentSeq(t, again); seq != "15" {
		t.Fatalf("the second review request is #%s, the page shows #15:\n%s", seq, again)
	}
	reviewer.run("say", "--reply", "15", "approve")
	expectLines(t, writer.run("task", "done", "Design approved"), "WRI-1 done on writer-reviewer. Working on: cleared.")

	// The person steps in from a terminal.
	expectLines(t, e.run("say", "--to", "mine", "@claude Also cover idempotency keys."),
		"Sent #18 to owner:alex on writer-reviewer", "@claude, @claude-2 get it now.")

	// More reviewers join by name in the reviewer role.
	if r := e.claudeSession("s-reviewer-2").run("join", "--board", "writer-reviewer", "--role", "reviewer"); !strings.HasPrefix(r.stdout, "Joined board writer-reviewer as claude-3 (reviewer, owner alex)\n") {
		t.Fatalf("a second reviewer joining:\n%s", r)
	}
}

// TestGuideUseCaseCoordinator runs docs/use-cases/coordinator.mdx.
func TestGuideUseCaseCoordinator(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	// The coordinator: board, tasks and brief.
	coordinator := e.claudeSession("s-coordinator")
	if r := coordinator.run("board", "new", "launch", "--title", "Launch checklist"); !strings.Contains(r.stdout, "Created board launch on ") || !strings.Contains(r.stdout, " and joined as claude (member, owner alex).") {
		t.Fatalf("board new:\n%s", r)
	}
	expectLines(t, coordinator.run("task", "new", "Write the release notes", "--no-start", "--about", "From the merged PRs since the last tag"),
		"Opened LAU-1 on launch: Write the release notes")
	expectLines(t, coordinator.run("task", "new", "Check the install script on Linux", "--no-start"),
		"Opened LAU-2 on launch: Check the install script on Linux")
	e.writeFile("brief.md", "# Launch checklist\n\nRelease notes and the Linux install check. Nobody is on a task yet.\n")
	expectLines(t, coordinator.run("brief", "put", "brief.md"), "Put the brief of launch as brief.md v1.")

	// The person switches the board to the recommended policy.
	expectLines(t, e.run("board", "policy", "recommended", "--board", "launch"), "Board launch is now on the recommended policy.")

	// Each agent: join, read the brief, pick a task, finish it.
	helper := e.claudeSession("s-helper")
	if r := helper.run("join", "--board", "launch"); !strings.HasPrefix(r.stdout, "Joined board launch as claude-2 (member, owner alex)\n") {
		t.Fatalf("join --board launch:\n%s", r)
	}
	if r := helper.run("brief"); !strings.Contains(r.stdout, "# Launch checklist") {
		t.Fatalf("brief:\n%s", r)
	}
	expectLines(t, helper.run("task", "list"),
		"launch · 2 open, 0 done",
		"Not picked up",
		"  LAU-1  Write the release notes",
		"  LAU-2  Check the install script on Linux")
	helper.run("task", "start", "LAU-1")
	helper.run("task", "note", "Drafted from 12 PRs")

	codex := e.codexSession("019a0000-0000-7000-8000-0000000000c1")
	if r := codex.run("join", "--board", "launch"); !strings.HasPrefix(r.stdout, "Joined board launch as codex (member, owner alex)\n") {
		t.Fatalf("codex join --board launch:\n%s", r)
	}
	codex.run("task", "start", "LAU-2")
	if r := codex.run("ask", "The install script fails on Alpine.\nShould Alpine block the launch?", "Yes, fix it first", "No, note it"); !strings.Contains(r.stdout, "LAU-2 is Blocked until @alex answers.") {
		t.Fatalf("the agent's ask:\n%s", r)
	}

	helper.run("task", "done", "Release notes in notes.md")
	if r := helper.run("say", "--to", "@claude", "LAU-1 is done: the release notes are in notes.md."); !strings.Contains(r.stdout, " to @claude on launch") {
		t.Fatalf("the report to the coordinator:\n%s", r)
	}

	// The coordinator: check progress and update the brief.
	expectLines(t, coordinator.run("task", "list"),
		"launch · 1 open, 1 done",
		"In progress",
		"  LAU-2  Check the install script on Linux · codex")
	coordinator.run("brief", "get", "brief.md")
	e.writeFile("brief.md", "# Launch checklist\n\nRelease notes done (LAU-1). Linux check waiting on Alpine (LAU-2).\n")
	expectLines(t, coordinator.run("brief", "put", "brief.md"), "Put the brief of launch as brief.md v2 (replaces v1).")
}

// TestGuideUseCaseClaudeCodeAndCodex runs docs/use-cases/claude-code-and-codex.mdx.
func TestGuideUseCaseClaudeCodeAndCodex(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if r := e.run("init", "--yes", "--allow-commands"); !strings.Contains(r.stdout, "claude-code, everywhere:") || !strings.Contains(r.stdout, "codex, everywhere:") {
		t.Fatalf("init:\n%s", r)
	}

	// Pair Claude Code with Codex.
	claude := e.claudeSession("s-build")
	pair := claude.run("pair").lines()
	if pair[1] != "Created board general and joined as claude (member, owner alex)" {
		t.Fatalf("pair:\n%s", strings.Join(pair, "\n"))
	}
	codex := e.codexSession("019a0000-0000-7000-8000-0000000000c2")
	if r := codex.run("join", pair[len(pair)-1]); !strings.HasPrefix(r.stdout, "Joined board general as codex (member, owner alex)\n") {
		t.Fatalf("codex join:\n%s", r)
	}

	// Claude Code: task and review request.
	claude.run("task", "new", "Add retries to the payment client")
	e.writeFile("retry.diff", "diff --git a/client.go b/client.go\n")
	expectLines(t, claude.run("say", "--to", "@codex", "--attach", "retry.diff", "--expect-reply", "The retry change is in retry.diff. Can you review it?"),
		"Sent #10 to @codex on general · about GEN-1", "@codex gets it now.")

	// Codex: read the diff and reply.
	if r := codex.run("inbox"); !strings.Contains(r.stdout, `harness="claude-code"`) || !strings.Contains(r.stdout, `Attached file "retry.diff" v1.`) {
		t.Fatalf("codex's inbox:\n%s", r)
	}
	codex.run("file", "get", "retry.diff", "review.diff")
	expectLines(t, codex.run("say", "--reply", "10", "One issue: the backoff has no cap. Otherwise approve."),
		"Sent #11 to @claude on general · about GEN-1", "@claude gets it now.")

	// Claude Code: acknowledge and finish.
	expectLines(t, claude.run("react", "11", "👀"), "Reacted 👀 to #11 on general · 👀 1")
	expectLines(t, claude.run("task", "done", "Retries in, reviewed by codex"), "GEN-1 done on general. Working on: cleared.")
}
