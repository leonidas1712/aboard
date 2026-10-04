//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// An agent reacts to a message by emoji or by name and takes it back; the reactions
// show on the message in read, in text and JSON, never reach anyone's inbox, and the
// record still verifies.
func TestReactShowsOnTheMessageAndNeverCountsUnread(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	ask := e.sayAs("writer", "--to", "@reviewer", "Can you review notes.md?")
	e.run("inbox", "--as", "reviewer")

	expectLines(t, e.asAgent("reviewer", "react", fmt.Sprint(ask), "👍"),
		fmt.Sprintf("Reacted 👍 to #%d on writer-reviewer · 👍 1", ask))
	expectLines(t, e.asAgent("critic", "react", fmt.Sprintf("#%d", ask), "thumbsup"),
		fmt.Sprintf("Reacted 👍 to #%d on writer-reviewer · 👍 2", ask))
	v := e.asAgent("reviewer", "react", fmt.Sprint(ask), "check", "--json").json(t)
	matchesCLISpec(t, "ReactOutput", v)
	if field(t, v, "reaction.name") != "check" || field(t, v, "reaction.emoji") != "✅" || field(t, v, "removed") != false {
		t.Fatalf("react --json: %v", v)
	}
	if got := field(t, v, "message.reactions.0.by"); fmt.Sprint(got) != "[reviewer critic]" {
		t.Fatalf("who reacted with 👍: %v", got)
	}
	if field(t, v, "message.reactions.0.mine") != true || field(t, v, "message.reactions.1.count") != float64(1) {
		t.Fatalf("react --json reactions: %v", field(t, v, "message.reactions"))
	}
	// Reacting twice with the same emoji changes nothing.
	expectLines(t, e.asAgent("reviewer", "react", fmt.Sprint(ask), "✅"),
		fmt.Sprintf("Reacted ✅ to #%d on writer-reviewer · 👍 2 ✅ 1", ask))

	// read shows the reactions last on the message's first line, in the set's order.
	expectLines(t, e.asAgent("writer", "read"), lines(
		"writer-reviewer · 1 message",
		fmt.Sprintf("#%d  @writer → @reviewer · 👍 2 ✅ 1", ask),
		"    writer · self",
		"    Can you review notes.md?",
	)...)
	page := e.asAgent("writer", "read", "--json").json(t)
	matchesCLISpec(t, "ReadOutput", page)
	if field(t, page, "messages.0.reactions.0.mine") != false {
		t.Fatalf("the writer didn't react, but mine is true: %v", page)
	}

	// Reactions are not messages: nobody has anything unread.
	for _, agent := range []string{"writer", "reviewer", "critic"} {
		in := e.run("inbox", "--as", agent, "--peek", "--json").json(t)
		if n := len(field(t, in, "messages").([]any)); n != 0 {
			t.Fatalf("%s has %d unread after reactions: %v", agent, n, in)
		}
	}

	// Taking a reaction back, and taking back one never made.
	expectLines(t, e.asAgent("critic", "react", fmt.Sprint(ask), "👍", "--remove"),
		fmt.Sprintf("Took back 👍 from #%d on writer-reviewer · 👍 1 ✅ 1", ask))
	expectLines(t, e.asAgent("critic", "react", fmt.Sprint(ask), "tada", "--remove"),
		fmt.Sprintf("Took back 🎉 from #%d on writer-reviewer · 👍 1 ✅ 1", ask))

	// The record holds each change, and still verifies.
	evs := e.ownerRequest("GET", "/v1/boards/writer-reviewer/events", nil)
	var types []string
	for _, ev := range evs["events"].([]any) {
		if typ := ev.(map[string]any)["type"].(string); strings.HasPrefix(typ, "reaction.") {
			types = append(types, typ)
		}
	}
	if want := "[reaction.added reaction.added reaction.added reaction.removed]"; fmt.Sprint(types) != want {
		t.Fatalf("reaction events: %v, want %s", types, want)
	}
	e.run("audit", "verify")

	if r := e.runExit("react", "--as", "reviewer", fmt.Sprint(ask), "🚀"); r.code != 2 || !strings.Contains(r.stderr, "👍 ✅ 👀 ❤️ 🎉 ❓") {
		t.Fatalf("an emoji outside the set: want a usage error naming the set\n%s", r)
	}
	if r := e.runExit("react", "--as", "reviewer", "99", "eyes", "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "message_ref_invalid" {
		t.Fatalf("react to no such message:\n%s", r)
	}
}

// Under addressed visibility an agent can't react to a message it may not see, and the
// record withholds reactions on it from that agent.
func TestReactionsUnderAddressedVisibilityDoNotLeak(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	e.run("board", "policy", "recommended")
	posted := field(t, e.run("say", "--as", "writer", "--to", "@reviewer", "only for the reviewer", "--json").json(t), "message").(map[string]any)
	seq := int(posted["seq"].(float64))
	e.asAgent("reviewer", "react", fmt.Sprint(seq), "eyes")

	if r := e.runExit("react", "--as", "critic", posted["id"].(string), "eyes", "--json"); r.code != 1 ||
		field(t, r.json(t), "error.code") != "message_ref_invalid" {
		t.Fatalf("the critic reacting to a message it may not see:\n%s", r)
	}
	// The critic verifies the whole record, with the reaction's payload withheld.
	if r := e.runExit("audit", "verify", "--as", "critic"); r.code != 0 {
		t.Fatalf("audit verify as the critic:\n%s", r)
	}
}

// A reaction never wakes a session: an idle Claude Code session stays asleep through
// one, and the next real message arrives alone in its bundle.
func TestReactionNeverWakesASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	writer, reviewer := pairedClaudeSessions(t, e)
	ask := int(field(t, writer.run("say", "--to", "@reviewer", "Draft is in notes.md.", "--json").json(t), "message.seq").(float64))
	reviewer.run("inbox")

	stop := writer.startHook("stop")
	if !stop.running(300 * time.Millisecond) {
		t.Fatalf("stop hook returned with nothing to deliver\n%s", stop.wait(time.Second))
	}
	reviewer.run("react", fmt.Sprint(ask), "thumbsup")
	if !stop.running(time.Second) {
		t.Fatalf("a reaction woke the writer's session\n%s", stop.wait(time.Second))
	}
	if n := writer.unread(); n != 0 {
		t.Fatalf("a reaction counts as unread: %d", n)
	}

	reviewer.run("say", "--to", "@writer", "Reviewed.")
	woke := stop.wait(5 * time.Second)
	if woke.code != 2 || !strings.Contains(woke.stderr, `count="1"`) || !strings.Contains(woke.stderr, "Reviewed.") ||
		strings.Contains(woke.stderr, "👍") || strings.Contains(woke.stderr, "thumbsup") {
		t.Fatalf("the bundle after a reaction should hold only the message\n%s", woke)
	}
}

// read --threads lists the messages that start threads, the newest activity first, with
// who else wrote in each.
func TestReadThreadsListsThreadsByLastActivity(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	plan := e.sayAs("writer", "Plan for today: first the parser, then the docs and the release notes.")
	ask := e.sayAs("writer", "--to", "@reviewer", "--expect-reply", "Can you take the tests?")
	e.sayAs("reviewer", "--reply", fmt.Sprint(ask), "Yes.")
	e.sayAs("critic", "--reply", fmt.Sprint(ask), "I'll help.")
	e.sayAs("writer", "Unrelated.")
	e.sayAs("critic", "--reply", fmt.Sprint(plan), "Sounds right.")

	expectLines(t, e.asAgent("reviewer", "read", "--threads"),
		"writer-reviewer · 2 threads",
		fmt.Sprintf(`#%d  @writer · 1 reply · last just now · critic · "Plan for today: first the parser, then t…"`, plan),
		fmt.Sprintf(`#%d  @writer · 2 replies · last just now · reviewer, critic · "Can you take the tests?"`, ask),
	)

	v := e.asAgent("reviewer", "read", "--threads", "--limit", "1", "--json").json(t)
	matchesCLISpec(t, "ReadThreadsOutput", v)
	if field(t, v, "more") != true || field(t, v, "threads.0.root.seq") != float64(plan) ||
		fmt.Sprint(field(t, v, "threads.0.participants")) != "[writer critic]" {
		t.Fatalf("read --threads --limit 1 --json: %v", v)
	}
	expectLines(t, e.asAgent("reviewer", "read", "--threads", "--limit", "1"),
		"writer-reviewer · 1 thread",
		fmt.Sprintf(`#%d  @writer · 1 reply · last just now · critic · "Plan for today: first the parser, then t…"`, plan),
		"Older threads: aboard read --threads --limit 2",
	)

	if r := e.runExit("read", "--as", "reviewer", "--threads", "--from", "@writer"); r.code != 2 || !strings.Contains(r.stderr, "--threads") {
		t.Fatalf("--threads with a filter: want a usage error\n%s", r)
	}
	if r := e.runExit("read", "--as", "reviewer", "--threads", "--thread", fmt.Sprint(ask)); r.code != 2 {
		t.Fatalf("--threads with --thread: want a usage error\n%s", r)
	}

	// A board without replies has no threads.
	f := newEnv(t)
	threeAgents(t, f)
	f.sayAs("writer", "Hello.")
	expectLines(t, f.asAgent("reviewer", "read", "--threads"), "writer-reviewer · no threads")
}
