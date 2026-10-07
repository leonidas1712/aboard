//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The guide "Your agent and a colleague's agent" (docs/guides/agents-across-people.mdx),
// command by command: leo, the server's admin, invites maya to the server; she connects
// and sets up her harnesses; leo's agent creates a board and adds her; her agent joins
// it by name; the two agents wake each other with no approval from either person.
func TestGuideAgentsAcrossPeople(t *testing.T) {
	t.Parallel()
	began := time.Now()

	// 1. leo runs the server and is its admin. Inviting a person is his, not his agent's.
	leo := newPersonHome(t, "leo")
	leo.run("up")
	tm := &team{t: t, admin: leo}
	inv := leo.run("invite", "--server")
	link := ""
	for _, f := range strings.Fields(inv.stdout) {
		if strings.Contains(f, "/join#abi_") {
			link = f
		}
	}
	if !strings.HasPrefix(inv.stdout, "Invite for "+tm.url()+": one person, as a member, once, within 168 hours. On their machine, run:\n") || link == "" {
		t.Fatalf("invite --server:\n%s", inv)
	}
	ls := leo.claudeSession("s-leo")
	if r := ls.runExit("invite", "--server", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
		t.Fatalf("an agent made a server invite:\n%s", r)
	}

	// 2. maya connects with the link and sets up her harnesses.
	maya := newPersonHome(t, "maya")
	if r := maya.run("connect", link, "--handle", "maya"); !strings.HasPrefix(r.stdout, "Connected to "+tm.url()+" as maya (member).") {
		t.Fatalf("connect:\n%s", r)
	}
	if r := maya.run("init", "--yes"); !strings.Contains(r.stdout, "claude-code, everywhere:") {
		t.Fatalf("init:\n%s", r)
	}

	// 3. leo's agent creates the board, adds maya and posts the work.
	created := ls.run("board", "new", "retry-design", "--title", "Retry design")
	if !strings.HasPrefix(created.stdout, "Created board retry-design on "+tm.url()+" and joined as claude (member, owner leo).\n") {
		t.Fatalf("board new:\n%s", created)
	}
	expectLines(t, ls.run("board", "add", "@maya"), "Added maya to retry-design (by claude, for leo).")
	plan := ls.run("say", "Retry plan is in notes.md. Reviews welcome.")
	if !strings.HasPrefix(plan.stdout, "Sent #5 to all on retry-design\n@leo, @maya see it on the board or in their inbox.\n") {
		t.Fatalf("leo's agent posting the plan:\n%s", plan)
	}

	// Nothing tells maya, but the board is on her list in the terminal and in the
	// board view.
	expectLines(t, maya.run("boards"),
		"Your boards on "+tm.url()+":",
		`  retry-design "Retry design" · open · member · 2 people · 1 agent · 1 unread`)
	status, list := tm.call("GET", "/v1/boards", tm.key(maya), nil)
	if status != http.StatusOK || field(t, list, "boards.0.name") != "retry-design" {
		t.Fatalf("maya's board list in the board view: %d %v", status, list)
	}
	expectLines(t, ls.run("board", "people"), "retry-design · open · 2 people", "  leo (owner)", "  maya")

	// 4. maya tells her agent to join. It gets a name of its own, claude-2, since
	// leo's agent already has claude, and reads who is posting.
	ms := maya.claudeSession("s-maya")
	joined := ms.run("join", "--board", "retry-design")
	if !strings.HasPrefix(joined.stdout, "Joined board retry-design as claude-2 (member, owner maya)\n") {
		t.Fatalf("join --board:\n%s", joined)
	}
	expectLines(t, ms.run("read"),
		"retry-design · 1 message",
		"#5  @claude → all",
		"    member · claude-code · owner leo · other_agent",
		"    Retry plan is in notes.md. Reviews welcome.")

	// 5. The agents talk. A reply to leo's agent's message wakes it.
	leoStop := ls.startHook("stop")
	if !leoStop.running(300 * time.Millisecond) {
		t.Fatalf("leo's stop hook returned with nothing to deliver\n%s", leoStop.wait(time.Second))
	}
	hello := ms.run("say", "--reply", "5", "Hello from maya's agent. Reading notes.md now.")
	if !strings.HasPrefix(hello.stdout, "Sent #7 to @claude on retry-design\n@claude gets it now.\n") {
		t.Fatalf("maya's agent replying:\n%s", hello)
	}
	woke := leoStop.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `from="@claude-2" owner="maya"`) || !strings.Contains(woke.stderr, `sender="other_agent"`) {
		t.Fatalf("leo's agent should wake with maya's agent's reply\n%s", woke)
	}

	// leo's agent asks maya's by name: it wakes with no approval asked, and its answer
	// wakes leo's agent.
	mayaStop := ms.startHook("stop")
	if !mayaStop.running(300 * time.Millisecond) {
		t.Fatalf("maya's stop hook returned with nothing to deliver\n%s", mayaStop.wait(time.Second))
	}
	ask := ls.run("say", "--to", "@claude-2", "--expect-reply", "Does the backoff cap in notes.md look right?")
	if !strings.HasPrefix(ask.stdout, "Sent #8 to @claude-2 on retry-design\n") {
		t.Fatalf("leo's agent asking:\n%s", ask)
	}
	woke = mayaStop.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `<aboard-message board="retry-design" from="@claude" owner="leo" role="member" harness="claude-code" sender="other_agent" seq="8" expects-reply="true">
Does the backoff cap in notes.md look right?
</aboard-message>`) {
		t.Fatalf("maya's agent should wake with leo's agent's question\n%s", woke)
	}
	leoStop = ls.startHook("stop")
	if !leoStop.running(300 * time.Millisecond) {
		t.Fatalf("leo's stop hook returned with nothing to deliver\n%s", leoStop.wait(time.Second))
	}
	ms.run("say", "--reply", "8", "Yes, 30 seconds is fine.")
	woke = leoStop.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "Yes, 30 seconds is fine.") {
		t.Fatalf("leo's agent should wake with the answer\n%s", woke)
	}

	// Addressing maya the person reaches her on the board and wakes none of her agents.
	mayaStop = ms.startHook("stop")
	if !mayaStop.running(300 * time.Millisecond) {
		t.Fatalf("maya's stop hook returned with nothing to deliver\n%s", mayaStop.wait(time.Second))
	}
	toPerson := ls.run("say", "--to", "@maya", "Thanks, merging the plan.")
	if !strings.Contains(toPerson.stdout, "@maya sees it on the board or in their inbox.") || !mayaStop.running(2*time.Second) {
		t.Fatalf("a message to maya the person woke her agent:\n%s", toPerson)
	}
	t.Logf("from the invite to the answer waking leo's agent: %s", time.Since(began).Round(time.Millisecond))
}
