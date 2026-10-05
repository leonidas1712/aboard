//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The mentions in a message (spec/events.md, "Mentions"): the server records who a
// message mentions as it posts it, a mention wakes the agent as if it were addressed,
// and it never changes who the message is to or who may read it.

// mentionsOf returns the recorded mentions of the message with seq, as reader reads
// the board, by name: what each was written as, whether it wakes and why not.
func mentionsOf(t *testing.T, e *env, reader string, seq int) map[string]string {
	t.Helper()
	for _, m := range field(t, e.run("read", "--as", reader, "--json").json(t), "messages").([]any) {
		m := m.(map[string]any)
		if int(m["seq"].(float64)) != seq {
			continue
		}
		out := map[string]string{}
		for _, mn := range m["mentions"].([]any) {
			mn := mn.(map[string]any)
			out[mn["name"].(string)] = fmt.Sprintf("%s %s %v %v", mn["kind"], mn["text"], mn["wakes"], mn["reason"])
		}
		return out
	}
	t.Fatalf("%s can't read #%d", reader, seq)
	return nil
}

// recipient finds one member in say --json's recipients.
func recipient(t *testing.T, out map[string]any, name string) map[string]any {
	t.Helper()
	for _, r := range field(t, out, "recipients").([]any) {
		if r := r.(map[string]any); r["name"] == name {
			return r
		}
	}
	t.Fatalf("say --json doesn't list %s among the recipients: %v", name, out["recipients"])
	return nil
}

// unreadSeqs lists an agent's unread messages without acknowledging them.
func unreadSeqs(t *testing.T, e *env, agent string) []int {
	t.Helper()
	var out []int
	for _, m := range field(t, e.run("inbox", "--as", agent, "--peek", "--json").json(t), "messages").([]any) {
		out = append(out, int(m.(map[string]any)["seq"].(float64)))
	}
	return out
}

// A message to everyone that mentions an idle agent in focused mode wakes it, and say
// says so: no warning that the message wakes no agent, and its `to` stays everyone.
func TestAMentionWakesAFocusedAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	stop := reviewer.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "")

	r := writer.run("say", "@reviewer can you look at the build?")
	expectLines(t, r, "Sent #6 to all on writer-reviewer",
		"@reviewer gets it now. @alex sees it on the board or in their inbox.")
	woke := stop.wait(10 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, "@reviewer can you look at the build?") ||
		strings.Contains(woke.stderr, "while you were away") {
		t.Fatalf("a mention in a message to everyone should wake the agent with it\n%s", woke)
	}
	if got := mentionsOf(t, e, "writer", 6); got["reviewer"] != "agent @reviewer true <nil>" || len(got) != 1 {
		t.Fatalf("recorded mentions: %v", got)
	}

	reviewer.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "")
	out := writer.run("say", "Thanks @reviewer, and @alex for the review.", "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if to := field(t, out, "message.to").([]any); len(to) != 1 || to[0] != "all" {
		t.Fatalf("a mention changed the message's to: %v", to)
	}
	if rv := recipient(t, out, "reviewer"); rv["mentioned"] != true || rv["outcome"] != "now" {
		t.Fatalf("the mentioned agent's outcome: %v", rv)
	}
	if alex := recipient(t, out, "alex"); alex["mentioned"] != true || alex["outcome"] != "person" {
		t.Fatalf("the mentioned person's outcome: %v", alex)
	}
	if out["warning"] != nil {
		t.Fatalf("warned that a message mentioning an agent wakes no agent: %v", out["warning"])
	}
}

// A mention in code, an escaped one, a name in an address or a link, and a name nobody
// on the board has are text: they wake no one and aren't recorded.
func TestTextThatIsNotAMentionWakesNoOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	stop := reviewer.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "")

	body := "Run `aboard say --to @reviewer` later.\n```\n@reviewer\n```\n" +
		`Not \@reviewer, not ops@reviewer.dev, not https://example.com/@reviewer, and @nobody is no one here.`
	out := writer.run("say", body, "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if got := field(t, out, "message.mentions").([]any); len(got) != 0 {
		t.Fatalf("text that isn't a mention was recorded as one: %v", got)
	}
	if field(t, out, "warning.code") != "wakes_no_agent" {
		t.Fatalf("a message to everyone that mentions no one should warn: %v", out["warning"])
	}
	if !stop.running(asleepFor) {
		t.Fatalf("text that isn't a mention woke the agent\n%s", stop.wait(time.Second))
	}
}

// @role:R names the role's members when the message is posted; an agent that takes the
// role later is not mentioned by it, and the record never changes.
func TestARoleMentionIsResolvedWhenPosted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	e.run("join", line)
	e.run("join", line, "--name", "critic")
	seq := e.sayAs("writer", "Over to @role:reviewer: please check notes.md.")
	e.run("join", line, "--name", "late")

	want := map[string]string{"reviewer": "agent @role:reviewer true <nil>", "critic": "agent @role:reviewer true <nil>"}
	if got := mentionsOf(t, e, "late", seq); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("mentions of @role:reviewer: %v, want %v", got, want)
	}
	for _, agent := range []string{"reviewer", "critic"} {
		if got := unreadSeqs(t, e, agent); fmt.Sprint(got) != fmt.Sprint([]int{seq}) {
			t.Fatalf("%s's inbox: %v, want #%d", agent, got, seq)
		}
	}
}

// An agent's delivery mode still decides: off and humans never wake it for another
// agent's mention, and say says it won't be woken.
func TestOffAndHumansModesWinOverAMention(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	for _, mode := range []string{"off", "humans"} {
		e.run("delivery", mode, "--as", "reviewer")
		stop := reviewer.startHook("stop")
		e.presenceIs("writer-reviewer", "reviewer", "idle", mode)
		out := writer.run("say", "@reviewer the build is red.", "--json").json(t)
		matchesCLISpec(t, "SayOutput", out)
		if rv := recipient(t, out, "reviewer"); rv["outcome"] != "not_woken" || rv["mentioned"] != true {
			t.Fatalf("%s: the mentioned agent's outcome: %v", mode, rv)
		}
		if !stop.running(asleepFor) {
			t.Fatalf("%s: a mention woke the agent\n%s", mode, stop.wait(time.Second))
		}
		_ = stop.cmd.Process.Kill()
	}
}

// Under addressed visibility a mention never lets an agent read a message: it is
// recorded as one that can't wake, the agent's inbox doesn't get it, and say says why.
func TestAMentionGrantsNoAccess(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	e.run("board", "policy", "recommended")

	r := e.run("say", "--as", "writer", "--to", "@reviewer", "Ask @critic if unsure.")
	expectLines(t, r, "Sent #8 to @reviewer on writer-reviewer",
		"@reviewer is disconnected: it sees it in its inbox or when its session reconnects. "+
			"@critic can't read it: on this board an agent reads only messages addressed to it, and a mention doesn't change that.")
	if got := mentionsOf(t, e, "writer", 8); got["critic"] != "agent @critic false cannot_read" {
		t.Fatalf("the mention of an agent that may not read the message: %v", got)
	}
	if got := unreadSeqs(t, e, "critic"); len(got) != 0 {
		t.Fatalf("a mention put a message the critic may not read in its inbox: %v", got)
	}
}

// One message's mentions wake at most eight agents: the rest are recorded, and say
// says they won't get it.
func TestMentionsWakeAtMostEightAgents(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	e.run("join", line)
	var names []string
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("a%d", i)
		e.run("join", line, "--name", name)
		names = append(names, "@"+name)
	}
	out := e.run("say", "--as", "writer", "--to", "@reviewer", "Heads up: "+strings.Join(names, " "), "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	seq := int(field(t, out, "message.seq").(float64))
	got := mentionsOf(t, e, "writer", seq)
	if got["a8"] != "agent @a8 true <nil>" || got["a9"] != "agent @a9 false limit" {
		t.Fatalf("mentions past the eighth agent: %v", got)
	}
	if rv := recipient(t, out, "a9"); rv["outcome"] != "over_limit" {
		t.Fatalf("the ninth mentioned agent's outcome: %v", rv)
	}
	if got := unreadSeqs(t, e, "a8"); fmt.Sprint(got) != fmt.Sprint([]int{seq}) {
		t.Fatalf("a8's inbox: %v, want #%d", got, seq)
	}
	if got := unreadSeqs(t, e, "a9"); len(got) != 0 {
		t.Fatalf("a mention past the limit reached a9's inbox: %v", got)
	}
}

// A mention in a reply brings in someone outside the thread without changing who the
// reply goes to: it reaches the mentioned agent's inbox as if addressed to it.
func TestAMentionInAReplyBringsSomeoneIn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	ask := e.sayAs("writer", "--to", "@reviewer", "--expect-reply", "Can you take the tests?")
	out := e.run("say", "--as", "reviewer", "--reply", fmt.Sprint(ask), "Done; @critic can you check it?", "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if to := field(t, out, "message.to").([]any); len(to) != 1 || to[0] != "@writer" {
		t.Fatalf("the mention changed the reply's recipients: %v", to)
	}
	if c := recipient(t, out, "critic"); c["mentioned"] != true || c["outcome"] != "no_session" {
		t.Fatalf("the mentioned critic's outcome: %v", c)
	}
	seq := int(field(t, out, "message.seq").(float64))
	if got := unreadSeqs(t, e, "critic"); fmt.Sprint(got) != fmt.Sprint([]int{seq}) {
		t.Fatalf("critic's inbox: %v, want #%d", got, seq)
	}
}

// The wakes_no_agent warning goes by what happens, not by who is mentioned: a message to
// everyone whose only mention is of an agent in off mode wakes no agent, so it warns.
func TestMentioningOnlyAnAgentInOffModeStillWarns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer, critic := e.claudeSession("s-writer"), e.claudeSession("s-reviewer"), e.claudeSession("s-critic")
	line := field(t, writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t), "join.line").(string)
	reviewer.run("join", line, "--name", "reviewer")
	critic.run("join", line, "--name", "critic")
	e.run("delivery", "off", "--as", "reviewer")
	reviewer.startHook("stop")
	critic.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "off")
	e.presenceIs("writer-reviewer", "critic", "idle", "")

	out := writer.run("say", "@reviewer the build is red.", "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if rv := recipient(t, out, "reviewer"); rv["outcome"] != "not_woken" || rv["mentioned"] != true {
		t.Fatalf("the mentioned agent in off mode: %v", rv)
	}
	if c := recipient(t, out, "critic"); c["outcome"] != "next_turn" {
		t.Fatalf("the agent nobody mentioned: %v", c)
	}
	if field(t, out, "warning.code") != "wakes_no_agent" {
		t.Fatalf("a message whose only mention is of an agent in off mode should warn: %v", out["warning"])
	}
}
