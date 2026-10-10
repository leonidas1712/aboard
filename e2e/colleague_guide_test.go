//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// The guide "Bring a colleague aboard" (docs/guides/bring-a-colleague-aboard.mdx),
// command by command, with stand-in Claude Code sessions: leo's agent makes a board and
// an invite for maya that waits for leo's approval; leo allows it; the agent collects
// the link and prompt itself; maya's agent sets her up from the one link and joins the
// board in that session; a hello and reply check messages get through, then the two
// agents talk. Then the names, a teammate already on the server, leo's own next
// session, and the invite commands the guide shows.
func TestGuideBringAColleagueAboard(t *testing.T) {
	t.Parallel()
	leo := newPersonHome(t, "leo")
	leo.run("up")
	tm := &team{t: t, admin: leo}
	url := tm.url()

	// 1. leo asks his agent to make the board and invite maya to it.
	ls := leo.claudeSession("s-leo")
	created := ls.run("board", "new", "qa", "--title", "QA")
	if !strings.HasPrefix(created.stdout, "Created board qa on "+url+" and joined as claude (member, owner leo).\n") {
		t.Fatalf("board new:\n%s", created)
	}
	held := ls.run("invite", "--person", "--handle", "maya", "--board", "qa")
	lines := held.lines()
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Pending approval apr_") || !strings.HasSuffix(lines[0], " on local · qa") ||
		!strings.HasPrefix(lines[1], "aboard approvals allow apr_") || !strings.HasSuffix(lines[1], " --server local") ||
		lines[2] != "Continue after your person allows or declines this exact action." {
		t.Fatalf("the held invite:\n%s", held)
	}
	approval := strings.Fields(lines[0])[2]

	// 2. leo approves it once from his terminal, which shows the link and prompt.
	if list := leo.run("approvals"); !strings.Contains(list.stdout, approval+" · invite people · @claude · board qa · pending") {
		t.Fatalf("approvals:\n%s", list)
	}
	allowed := leo.run("approvals", "allow", approval, "--server", url)
	shown := allowed.lines()
	if len(shown) != 3 || shown[0] != approval+" · executed on local" || !strings.HasPrefix(shown[1], "Invite: "+url+"/join#abi_") {
		t.Fatalf("approvals allow:\n%s", allowed)
	}
	link := strings.TrimPrefix(shown[1], "Invite: ")
	if !strings.HasPrefix(shown[2], "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh") ||
		!strings.Contains(shown[2], "If aboard version is older than 0.1.4, run aboard upgrade first.") ||
		!strings.Contains(shown[2], "aboard skill") || !strings.Contains(shown[2], "aboard setup "+link+" --handle maya.") {
		t.Fatalf("the prompt for maya:\n%s", allowed)
	}

	// 3. The agent is told, and collects the link and prompt itself, once.
	notice := ls.hook("prompt", `"prompt":"continue"`)
	if !strings.Contains(notice.stdout, "aboard approvals show '"+approval+"'") || strings.Contains(notice.stdout, "abi_") {
		t.Fatalf("the agent's notice:\n%s", notice)
	}
	collected := ls.run("approvals", "show", approval).lines()
	if len(collected) != 3 || !strings.HasPrefix(collected[0], approval+" · executed on ") || !strings.HasSuffix(collected[0], " · qa") ||
		collected[1] != "Invite: "+link || collected[2] != shown[2] {
		t.Fatalf("approvals show:\n%s", strings.Join(collected, "\n"))
	}
	if again := ls.run("approvals", "show", approval); !strings.HasPrefix(again.stdout, collected[0]+"\naboard invite revoke inv_") ||
		!strings.Contains(again.stdout, " --server local\nAlready collected, revoked or expired.") ||
		!strings.Contains(again.stdout, "aboard invite --person --server local --board qa") || strings.Contains(again.stdout, "abi_") {
		t.Fatalf("a second approvals show:\n%s", again)
	}

	// 4. maya's agent installs the skill and sets her up from the one link.
	maya := newPersonHome(t, "maya")
	ms := maya.claudeSession("s-maya")
	if r := ms.run("skill"); !strings.Contains(r.stdout, "# Aboard") {
		t.Fatalf("skill:\n%s", r)
	}
	set := ms.run("setup", link, "--handle", "maya")
	expectLines(t, set,
		"Setup on "+url+": pending",
		"installed: complete · Aboard is installed; its owner can update it.",
		"account: complete · Your account was created and its saved key was verified.",
		"memberships: complete · Current accessible memberships were checked; removed access was not recreated.",
		"harness: pending · Harness configuration needs trust or restart confirmation.",
		"joining: complete · This exact session joined the invited boards; existing seats were reused.",
		"delivery: pending · Delivery has not been verified by a session round trip.",
		"aboard init --harness claude-code",
		"Run aboard skill now; it loads automatically in your next session. Run /hooks, approve Aboard's hooks, then restart Claude Code. Continue Aboard setup.")
	if strings.Contains(set.stdout+set.stderr, link) || strings.Contains(set.stdout+set.stderr, tm.key(maya)) {
		t.Fatal("setup printed the invite or the key")
	}

	// After the hooks are trusted and Claude Code restarted, setup confirms them and
	// waits for a reply to its hello.
	ms = maya.claudeSessionFrom("s-maya", "resume")
	waiting := ms.run("setup", "--continue")
	if !strings.Contains(waiting.stdout, "harness: complete") || !strings.Contains(waiting.stdout, "delivery: pending · Waiting for a reply from @leo's agents.\n") {
		t.Fatalf("setup after the restart:\n%s", waiting)
	}

	// 5. The hello reaches leo's agent, which replies; setup then reports complete.
	hello := ls.startHook("stop").wait(10 * time.Second)
	if hello.code != 2 || !strings.Contains(hello.stderr, `from="@claude-2" owner="maya" role="member" harness="claude-code" sender="other_agent"`) ||
		!strings.Contains(hello.stderr, "Hello, I joined using your invitation. Please reply so I can check that messages get through.") {
		t.Fatalf("setup's hello did not reach the inviting agent:\n%s", hello)
	}
	seq := strings.TrimSuffix(strings.Fields(hello.stderr[strings.Index(hello.stderr, ` seq="`):])[0][len(`seq="`):], `"`)
	ls.run("say", "--reply", seq, "Hi maya, welcome.")
	if got := ms.startHook("stop").wait(10 * time.Second); got.code != 2 || !strings.Contains(got.stderr, "Hi maya, welcome.") {
		t.Fatalf("the reply did not reach the newcomer:\n%s", got)
	}
	verified := ms.run("setup", "--continue")
	if !strings.HasPrefix(verified.stdout, "Setup on "+url+": complete\n") || !strings.Contains(verified.stdout, "delivery: complete · Messages get through on the invited board.\n") {
		t.Fatalf("setup did not report the reply:\n%s", verified)
	}

	// 6. The agents talk; each labels the other other_agent.
	people := ms.run("board", "people").lines()
	if len(people) != 5 || !strings.HasSuffix(people[0], "qa · open · 2 people") || people[1] != "  leo (owner)" ||
		!strings.HasPrefix(people[2], "    @claude · claude-code · ") || people[3] != "  maya" || !strings.HasPrefix(people[4], "    @claude-2 · claude-code · ") {
		t.Fatalf("board people from maya's agent:\n%s", strings.Join(people, "\n"))
	}
	leoStop := ls.startHook("stop")
	if !leoStop.running(300 * time.Millisecond) {
		t.Fatalf("leo's stop hook returned with nothing to deliver\n%s", leoStop.wait(time.Second))
	}
	ms.run("say", "--to", "@claude", "--expect-reply", "Ready when you are. What should I review first?")
	if woke := leoStop.wait(10 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, `sender="other_agent"`) {
		t.Fatalf("leo's agent should wake with maya's agent's message\n%s", woke)
	}

	// Names: maya's agent renames its own person.
	expectLines(t, ms.run("people", "rename", "@maya", "maya-k"),
		"@maya is now @maya-k on "+url+". Their identity, boards and agents stay.")

	// Someone already on the server: added to the board, then their agent joins it.
	sam := tm.person("sam")
	expectLines(t, ls.run("board", "add", "@sam", "--board", "qa"), "Added sam to qa (by claude, for leo).")
	if r := sam.claudeSession("s-sam").run("join", "--board", "qa"); !strings.HasPrefix(r.stdout, "Joined board qa as claude-") || !strings.Contains(r.stdout, " (member, owner sam)\n") {
		t.Fatalf("sam's agent joining qa:\n%s", r)
	}

	// leo's own next session joins by name.
	if r := leo.claudeSession("s-leo-2").run("join", "--board", "qa"); !strings.HasPrefix(r.stdout, "Joined board qa as claude-") {
		t.Fatalf("leo's next session joining qa:\n%s", r)
	}

	// Auto mode for inviting, editing the suggested name, the agent's reads, and
	// revoking an invite.
	expectLines(t, leo.run("allowance", "set", "invite-people", "on"),
		"Allowance on "+url+": invite-people",
		"Agents allowed to invite people can let outsiders read every open board.")
	auto := ls.run("invite", "--person", "--handle", "sam-2", "--board", "qa")
	if !strings.Contains(auto.stdout, " · executed on local · qa\n") {
		t.Fatalf("an invite under the allowance:\n%s", auto)
	}
	invites := ls.run("invite", "list")
	var active string
	for _, line := range invites.lines() {
		if strings.Contains(line, " · active · for @sam-2 · issued by agent @claude · ") {
			active = strings.Fields(line)[0]
		}
	}
	if active == "" || !strings.Contains(invites.stdout, " · redeemed · for @maya · issued by agent @claude · ") || strings.Contains(invites.stdout, "abi_") {
		t.Fatalf("invite list from the agent:\n%s", invites)
	}
	expectLines(t, ls.run("invite", "edit", active, "--handle", "sam-k"),
		"Updated suggested handle for invitation "+active+" on "+url+".")
	if r := ls.run("people"); !strings.Contains(r.stdout, "@maya-k") || !strings.Contains(r.stdout, "@sam") {
		t.Fatalf("people from the agent:\n%s", r)
	}
	expectLines(t, leo.run("invite", "revoke", active), "Revoked invitation "+active+" on "+url+".")
	if r := leo.run("invite", "list"); !strings.Contains(r.stdout, active+" · revoked") {
		t.Fatalf("invite list after revoke:\n%s", r)
	}
	expectLines(t, leo.run("allowance", "off"), "Allowance on "+url+": off")
}

// The "Hand work to someone else's session" section of docs/guides/pairing.mdx: a
// pairing request to a teammate on the board, listed and accepted in the session she
// chooses, then another declined and one cancelled.
func TestGuidePairingRequestToSomeoneElsesSession(t *testing.T) {
	t.Parallel()
	leo := newPersonHome(t, "leo")
	leo.run("up")
	tm := &team{t: t, admin: leo}
	quoted := "'" + tm.url() + "'"
	maya := tm.person("maya")
	ls := leo.claudeSession("s-leo")
	ls.run("board", "new", "writer-reviewer")
	ls.run("board", "add", "@maya", "--board", "writer-reviewer")

	request := ls.run("pairing", "request", "@maya", "--board", "writer-reviewer", "Review the retry change")
	id := strings.Fields(request.stdout)[0]
	expectLines(t, request, id+" · board writer-reviewer · awaiting_endpoint", "aboard pairing list --server "+quoted)
	ms := maya.claudeSession("s-maya")
	if r := ms.run("pairing", "list"); !strings.Contains(r.stdout, id+" · board writer-reviewer · awaiting_endpoint\n") || strings.Contains(r.stdout, "brd_") {
		t.Fatalf("maya's pairing list:\n%s", r)
	}
	if r := ms.run("pairing", "accept", id, "--here"); !strings.HasPrefix(r.stdout, id+" · board writer-reviewer · ") {
		t.Fatalf("pairing accept:\n%s", r)
	}

	declined := strings.Fields(ls.run("pairing", "request", "@maya", "--board", "writer-reviewer", "Another review").stdout)[0]
	if r := ms.run("pairing", "decline", declined); !strings.HasPrefix(r.stdout, declined+" · board writer-reviewer · declined") {
		t.Fatalf("pairing decline:\n%s", r)
	}
	cancelled := strings.Fields(ls.run("pairing", "request", "@maya", "--board", "writer-reviewer", "One more").stdout)[0]
	if r := ls.run("pairing", "cancel", cancelled); !strings.HasPrefix(r.stdout, cancelled+" · board writer-reviewer · cancelled") {
		t.Fatalf("pairing cancel:\n%s", r)
	}
}
