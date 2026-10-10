//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// The guide "Pair with a colleague's agent" (docs/guides/pair-with-a-colleague.mdx),
// command by command, with stand-in Claude Code sessions: leo's agent makes a board and
// a bundled invite that waits for leo's approval; leo allows it; maya's agent sets her up
// from the one link and accepts the pairing in its own session; each session receives
// the other's delivery check; the two agents talk. Then pairing again with maya, and
// with leo's own next session, and the invite and allowance commands the guide shows.
// The verified ready state needs real harness sessions: e2e/live
// TestInvitedSetupVerifiesTwoPeopleExactSessions.
func TestGuidePairWithAColleague(t *testing.T) {
	t.Parallel()
	leo := newPersonHome(t, "leo")
	leo.run("up")
	tm := &team{t: t, admin: leo}
	url := tm.url()
	quoted := "'" + url + "'"

	// 1. leo asks his agent to make the board and invite his colleague to it.
	ls := leo.claudeSession("s-leo")
	created := ls.run("board", "new", "pairing-test", "--title", "Pairing test")
	if !strings.HasPrefix(created.stdout, "Created board pairing-test on "+url+" and joined as claude (member, owner leo).\n") {
		t.Fatalf("board new:\n%s", created)
	}
	work := "Check that our agents can message each other"
	held := ls.run("invite", "--person", "--board", "pairing-test", "--pairing", work)
	lines := held.lines()
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Pending approval apr_") || !strings.HasSuffix(lines[0], " on "+url+" · pairing-test") ||
		!strings.HasPrefix(lines[1], "aboard approvals allow apr_") || !strings.HasSuffix(lines[1], " --server "+quoted) ||
		lines[2] != "Continue after your person allows or declines this exact action." {
		t.Fatalf("the held invite:\n%s", held)
	}
	approval := strings.Fields(lines[0])[2]

	// 2. leo allows it from his terminal, and his agent takes the pairing in its session.
	if list := leo.run("approvals"); !strings.Contains(list.stdout, approval+" · invite people · @claude · board pairing-test · pending") {
		t.Fatalf("approvals:\n%s", list)
	}
	allowed := leo.run("approvals", "allow", approval, "--json")
	approved := allowed.json(t)
	link := field(t, approved, "invite.link").(string)
	request := field(t, approved, "invite.pairing_request_id").(string)
	if field(t, approved, "approval.state") != "executed" || !strings.HasPrefix(link, url+"/join#abi_") || request == "" ||
		field(t, approved, "invite.prompt") != "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh, run aboard skill, then run aboard setup "+link+" --handle <name you'd like teammates to see>. Verify you can exchange messages with the inviting agent." {
		t.Fatalf("approvals allow:\n%s", allowed)
	}
	if r := ls.run("pairing", "select", request, "--here"); !strings.HasPrefix(r.stdout, request+" · board pairing-test · awaiting_account") {
		t.Fatalf("pairing select:\n%s", r)
	}

	// 3. maya's agent reads the skill and sets her up from the one link. It asks for her
	// name before it uses the invite.
	maya := newPersonHome(t, "maya")
	ms := maya.claudeSession("s-maya")
	if r := ms.run("skill"); !strings.Contains(r.stdout, "# Aboard") {
		t.Fatalf("skill:\n%s", r)
	}
	waiting := ms.run("setup", link)
	expectLines(t, waiting,
		"Setup on "+url+": pending",
		"installed: complete · Aboard is installed; its owner can update it.",
		"account: pending · Your visible name needs your person's choice; the invite has not been used.",
		"memberships: pending",
		"harness: pending",
		"joining: pending",
		"delivery: pending",
		"aboard setup --continue --handle maya",
		"Ask your person what name they'd like teammates to see. Suggested name: maya (availability is checked when you continue). Set --handle to their chosen name, then Continue Aboard setup.")
	set := ms.run("setup", "--continue", "--handle", "maya")
	expectLines(t, set,
		"Setup on "+url+": pending",
		"installed: complete · Aboard is installed; its owner can update it.",
		"account: complete · Your account was created and its saved key was verified.",
		"memberships: complete · Current board access and delivery participation were checked.",
		"harness: pending · Harness configuration needs trust or restart confirmation.",
		"joining: complete · This exact session joined the invited boards.",
		"delivery: pending · Both current sessions' round trips are not yet verified.",
		"aboard init --harness claude-code",
		"Run aboard skill now; it loads automatically in your next session. Run /hooks, approve Aboard's hooks, then restart Claude Code. Continue Aboard setup.")
	if strings.Contains(set.stdout+set.stderr, link) || strings.Contains(set.stdout+set.stderr, tm.key(maya)) {
		t.Fatal("setup printed the invite or the key")
	}

	// 4. Each session gets the other's delivery check and replies to it. Nothing is
	// ready until both round trips are confirmed.
	leoStop, mayaStop := ls.startHook("stop"), ms.startHook("stop")
	toLeo, toMaya := leoStop.wait(10*time.Second), mayaStop.wait(10*time.Second)
	for _, c := range []struct {
		woke result
		from string
		dir  string
	}{{toLeo, `from="@claude-2" owner="maya"`, "recipient_to_initiator"}, {toMaya, `from="@claude" owner="leo"`, "initiator_to_recipient"}} {
		if c.woke.code != 2 || !strings.Contains(c.woke.stderr, c.from) || !strings.Contains(c.woke.stderr, `sender="other_agent"`) ||
			!strings.Contains(c.woke.stderr, "ABOARD-PAIRING "+request+" generation=1 direction="+c.dir+" kind=ping") {
			t.Fatalf("delivery check:\n%s", c.woke)
		}
	}
	reply := func(s *session, woke result, to, dir string) {
		t.Helper()
		seq := strings.TrimSuffix(strings.Fields(woke.stderr[strings.Index(woke.stderr, ` seq="`):])[0][len(`seq="`):], `"`)
		s.run("say", "--reply", seq, "--to", to, "ABOARD-PAIRING "+request+" generation=1 direction="+dir+" kind=reply")
	}
	if r := ls.run("pairing", "list"); !strings.Contains(r.stdout, request+" · ") || strings.Contains(r.stdout, "ready") {
		t.Fatalf("pairing list before both sessions replied:\n%s", r)
	}
	reply(ls, toLeo, "@claude-2", "recipient_to_initiator")
	reply(ms, toMaya, "@claude", "initiator_to_recipient")
	for _, s := range []*session{ls, ms} {
		if got := s.startHook("stop").wait(10 * time.Second); got.code != 2 || !strings.Contains(got.stderr, "kind=reply") {
			t.Fatalf("the other side's reply to the delivery check:\n%s", got)
		}
	}
	// Each session's next turn end confirms the reply it was handed; then the request is
	// ready, and setup says delivery is verified.
	idle := []*proc{ls.startHook("stop"), ms.startHook("stop")}
	eventually(t, 10*time.Second, "the pairing request ready once both round trips are confirmed", func() bool {
		for _, line := range ls.run("pairing", "list").lines() {
			if strings.HasPrefix(line, request+" · ") && strings.HasSuffix(line, " · ready") {
				return true
			}
		}
		return false
	})
	for _, p := range idle {
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	expectLines(t, ms.run("setup", "--continue"),
		"Setup on "+url+": complete",
		"installed: complete · Aboard is installed; its owner can update it.",
		"account: complete · Your account was created and its saved key was verified.",
		"memberships: complete · Current board access and delivery participation were checked.",
		"harness: complete · The current session's hooks or extension were confirmed by the delivery daemon. Run aboard skill now; it loads automatically in your next session.",
		"joining: complete · This exact session joined the invited boards.",
		"delivery: complete · Both current-generation session round trips were verified.")

	// 5. The agents talk; each labels the other other_agent.
	people := ms.run("board", "people").lines()
	if len(people) != 5 || !strings.HasSuffix(people[0], "pairing-test · open · 2 people") || people[1] != "  leo (owner)" ||
		!strings.HasPrefix(people[2], "    @claude · claude-code · ") || people[3] != "  maya" || !strings.HasPrefix(people[4], "    @claude-2 · claude-code · ") {
		t.Fatalf("board people from maya's agent:\n%s", strings.Join(people, "\n"))
	}
	leoStop = ls.startHook("stop")
	if !leoStop.running(300 * time.Millisecond) {
		t.Fatalf("leo's stop hook returned with nothing to deliver\n%s", leoStop.wait(time.Second))
	}
	ms.run("say", "--to", "@claude", "--expect-reply", "Ready when you are. What should I review first?")
	if woke := leoStop.wait(10 * time.Second); woke.code != 2 || !strings.Contains(woke.stderr, `from="@claude-2" owner="maya" role="member" harness="claude-code" sender="other_agent"`) {
		t.Fatalf("leo's agent should wake with maya's agent's message\n%s", woke)
	}

	// Pair again with maya, who is already on the server: a pairing request, accepted
	// in the session she chooses.
	again := ls.run("pairing", "request", "@maya", "--board", "pairing-test", "Review the retry change")
	expectLines(t, again, strings.Fields(again.stdout)[0]+" · board pairing-test · awaiting_endpoint", "aboard pairing list --server "+quoted)
	second := strings.Fields(again.stdout)[0]
	ms2 := maya.claudeSession("s-maya-2")
	if r := ms2.run("pairing", "list"); !strings.Contains(r.stdout, second+" · board pairing-test · awaiting_endpoint\n") || strings.Contains(r.stdout, "brd_") {
		t.Fatalf("maya's pairing list:\n%s", r)
	}
	if r := ms2.run("pairing", "accept", second, "--here"); !strings.HasPrefix(r.stdout, second+" · board pairing-test · ") {
		t.Fatalf("pairing accept:\n%s", r)
	}

	third := strings.Fields(ls.run("pairing", "request", "@maya", "--board", "pairing-test", "Another review").stdout)[0]
	if r := ms2.run("pairing", "decline", third); !strings.HasPrefix(r.stdout, third+" · board pairing-test · declined") {
		t.Fatalf("pairing decline:\n%s", r)
	}

	// Pair with leo's own next session.
	own := ls.run("pairing", "request", "me", "--board", "pairing-test", "Pick up the auth review")
	ownID := strings.Fields(own.stdout)[0]
	expectLines(t, own, ownID+" · board pairing-test · awaiting_endpoint", "aboard pairing accept "+ownID+" --here --server "+quoted)
	ls2 := leo.claudeSession("s-leo-2")
	if r := ls2.run("pairing", "list"); !strings.Contains(r.stdout, ownID+" · ") {
		t.Fatalf("leo's next session's pairing list:\n%s", r)
	}
	if r := ls2.run("pairing", "accept", ownID, "--here"); !strings.HasPrefix(r.stdout, ownID+" · board pairing-test · ") {
		t.Fatalf("own pairing accept:\n%s", r)
	}

	// Auto mode for inviting, the invite notice, and revoking it.
	expectLines(t, leo.run("allowance", "set", "invite-people", "on"),
		"Allowance on "+url+": invite-people",
		"Agents allowed to invite people can let outsiders read every open board.")
	auto := ls.run("invite", "--person", "--board", "pairing-test")
	if !strings.Contains(auto.stdout, " · executed on "+url+" · pairing-test\n") {
		t.Fatalf("an invite under the allowance:\n%s", auto)
	}
	invites := leo.run("invite", "list")
	var active string
	for _, line := range invites.lines() {
		if strings.Contains(line, " · active · issued by agent ") {
			active = strings.Fields(line)[0]
		}
	}
	if !strings.Contains(invites.stdout, " · issued by agent @claude · ") || strings.Contains(invites.stdout, "mem_") {
		t.Fatalf("invite list shows ids instead of names:\n%s", invites)
	}
	if active == "" {
		t.Fatalf("invite list:\n%s", invites)
	}
	leo.run("invite", "revoke", active)
	if r := leo.run("invite", "list"); !strings.Contains(r.stdout, active+" · revoked") {
		t.Fatalf("invite list after revoke:\n%s", r)
	}
	expectLines(t, leo.run("allowance", "off"), "Allowance on "+url+": off")
}
