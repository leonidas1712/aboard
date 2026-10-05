//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// The rule for each delivery mode, as every place an agent learns about its seat writes
// it (DeliveryRule in spec/cli.yaml).
const (
	focusedRule = "A message to everyone wakes only the agents it mentions in focused mode, you included; the others get it quietly at their next turn. " +
		"To make an agent act soon, address or mention it (--to @name, --to role:R, or @name in the text) or ask with --expect-reply."
	allRule = "Every message wakes you, and every other agent in all mode, so post to everyone sparingly " +
		"and address the agents a message is for (--to @name or --to role:R)."
	humansRule = "Only messages from people wake you; messages from agents wait until a person's message wakes you, " +
		"or until you run aboard inbox."
)

// pair, join, resume and status each tell the agent its delivery mode and what the mode
// means for how it addresses messages, in text and in --json.
func TestTakingASeatNamesTheDeliveryModeAndItsRule(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := e.claudeSession("s-writer"), e.claudeSession("s-reviewer")

	paired := writer.run("pair", "writer-reviewer", "--name", "writer", "--json").json(t)
	matchesCLISpec(t, "PairOutput", paired)
	if field(t, paired, "delivery") != "focused" || field(t, paired, "delivery_rule") != focusedRule {
		t.Fatalf("pair --json: delivery %v, rule %v", paired["delivery"], paired["delivery_rule"])
	}
	line := field(t, paired, "join.line").(string)

	joined := reviewer.run("join", line, "--name", "reviewer")
	expectLines(t, joined,
		"Joined board writer-reviewer as reviewer (owner alex)",
		"This session acts as reviewer, and messages for reviewer arrive here.",
		"Delivery mode: focused. "+focusedRule)

	third := e.claudeSession("s-third")
	out := third.run("join", line, "--name", "third", "--json").json(t)
	matchesCLISpec(t, "JoinOutput", out)
	if field(t, out, "delivery") != "focused" || field(t, out, "delivery_rule") != focusedRule {
		t.Fatalf("join --json: delivery %v, rule %v", out["delivery"], out["delivery_rule"])
	}

	e.run("delivery", "all", "--as", "reviewer")
	other := e.claudeSession("s-other")
	expectLines(t, other.run("resume", "reviewer"),
		"Resumed reviewer on writer-reviewer in this session.",
		"Delivery mode: all. "+allRule)
	out = other.run("resume", "reviewer", "--json").json(t)
	matchesCLISpec(t, "ResumeOutput", out)
	if field(t, out, "delivery") != "all" || field(t, out, "delivery_rule") != allRule {
		t.Fatalf("resume --json: delivery %v, rule %v", out["delivery"], out["delivery_rule"])
	}

	status := other.run("status")
	if !strings.Contains(status.stdout, "; delivery all;") || !strings.Contains(status.stdout, "\n        "+allRule+"\n") {
		t.Fatalf("status doesn't show the rule under the Agent line:\n%s", status.stdout)
	}
	out = other.run("status", "--json").json(t)
	matchesCLISpec(t, "StatusOutput", out)
	if field(t, out, "delivery_rule") != allRule {
		t.Fatalf("status --json delivery_rule: %v", out["delivery_rule"])
	}
	if out := e.run("status", "--json").json(t); out["delivery_rule"] != nil {
		t.Fatalf("status with no agent has a delivery rule: %v", out["delivery_rule"])
	}
}

// A message to everyone that wakes no agent ends aboard say with a warning naming the
// fix; one addressed to an agent, or asking for a reply, doesn't.
func TestSayWarnsWhenAMessageToEveryoneWakesNoAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	reviewer.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "")

	r := writer.run("say", "FYI: the build is green.")
	got := r.lines()
	want := "Warning (wakes_no_agent): No agent wakes for this message to everyone; agents in focused mode see it at their next turn. " +
		"To make one act soon, mention it (@name in the text), send it with --to @name or --to role:R, or ask with --expect-reply."
	if len(got) < 2 || got[len(got)-2] != "@reviewer sees it at its next turn. @alex sees it on the board or in their inbox." || got[len(got)-1] != want {
		t.Fatalf("say to everyone in focused mode should end with the warning:\n%s", r.stdout)
	}
	out := writer.run("say", "FYI: and the docs build too.", "--json").json(t)
	matchesCLISpec(t, "SayOutput", out)
	if field(t, out, "warning.code") != "wakes_no_agent" || !strings.Contains(field(t, out, "warning.hint").(string), "--to @name") {
		t.Fatalf("say --json warning: %v", out["warning"])
	}

	for _, args := range [][]string{
		{"say", "--to", "@reviewer", "Can you look at the build?", "--json"},
		{"say", "--expect-reply", "Has anyone seen the flaky test?", "--json"},
	} {
		out := writer.run(args...).json(t)
		matchesCLISpec(t, "SayOutput", out)
		if out["warning"] != nil {
			t.Fatalf("%v: warned for a message that wakes an agent: %v", args, out["warning"])
		}
	}

	e.run("delivery", "all", "--as", "reviewer")
	reviewer.startHook("stop")
	e.presenceIs("writer-reviewer", "reviewer", "idle", "all")
	if out := writer.run("say", "FYI: one more thing.", "--json").json(t); out["warning"] != nil {
		t.Fatalf("warned for a message to everyone that wakes an agent in all mode: %v", out["warning"])
	}
}

// A changed delivery mode doesn't wake the session; its next bundle starts with a line
// saying the mode changed and what the new one means, once.
func TestAChangedModeComesWithTheNextBundle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)

	stop := reviewer.startHook("stop")
	expectLines(t, e.run("delivery", "all", "--as", "reviewer"),
		"reviewer on writer-reviewer: delivery now all (wakes for every message)")
	if !stop.running(asleepFor) {
		t.Fatalf("a changed mode woke the session\n%s", stop.wait(time.Second))
	}
	writer.run("say", "FYI: the build is green.")
	woke := stop.wait(10 * time.Second)
	if woke.code != 2 {
		t.Fatalf("in all mode a message to everyone should wake the session\n%s", woke)
	}
	if want := "Aboard: your delivery mode on writer-reviewer changed from focused to all. " + allRule + "\n"; !strings.HasPrefix(woke.stderr, want) {
		t.Fatalf("the bundle doesn't start with the changed mode:\n%s", woke.stderr)
	}

	stop = reviewer.startHook("stop")
	writer.run("say", "FYI: and the docs build too.")
	if again := stop.wait(10 * time.Second); strings.Contains(again.stderr, "delivery mode") {
		t.Fatalf("the next bundle told the changed mode again:\n%s", again.stderr)
	}

	// Changed and changed back before the session hears of it: nothing to tell.
	e.run("delivery", "humans", "--as", "reviewer")
	e.run("delivery", "all", "--as", "reviewer")
	if text := reviewer.hook("prompt", `"prompt":"next"`).stdout; strings.Contains(text, "delivery mode") {
		t.Fatalf("a mode changed back was told:\n%s", text)
	}
}
