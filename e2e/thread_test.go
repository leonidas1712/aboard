//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// TestReadThreadShowsTheWholeThreadFromAnyMessageInIt reads a thread with a reply to a
// reply from each of its messages, in text, JSON and Markdown, and checks that the
// timeline counts the thread's replies on its first message.
func TestReadThreadShowsTheWholeThreadFromAnyMessageInIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("join", field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string))
	ask := e.sayAs("writer", "--to", "@reviewer", "--expect-reply", "Can you take the tests?")
	yes := e.sayAs("reviewer", "--reply", fmt.Sprint(ask), "Yes. The build is broken first.")
	other := e.sayAs("writer", "Unrelated.")
	thanks := e.sayAs("writer", "--reply", fmt.Sprint(yes), "Thanks.")

	thread := lines(
		fmt.Sprintf("writer-reviewer · thread #%d · 2 replies", ask),
		fmt.Sprintf("#%d  @writer → @reviewer · asks for a reply · 2 replies", ask),
		"    writer · owner_agent",
		"    Can you take the tests?",
		fmt.Sprintf("#%d  @reviewer → @writer · reply to #%d", yes, ask),
		"    reviewer · self",
		"    Yes. The build is broken first.",
		fmt.Sprintf("#%d  @writer → @reviewer · reply to #%d", thanks, yes),
		"    writer · owner_agent",
		"    Thanks.",
	)
	for _, n := range []int{ask, yes, thanks} {
		expectLines(t, e.asAgent("reviewer", "read", "--thread", fmt.Sprint(n)), thread...)
	}
	expectLines(t, e.asAgent("reviewer", "read", "--thread", fmt.Sprintf("#%d", thanks)), thread...)

	// The timeline shows how many replies a thread has on its first message.
	expectLines(t, e.asAgent("reviewer", "read", "--limit", "4"), lines(
		"writer-reviewer · 4 messages",
		thread[1:7],
		fmt.Sprintf("#%d  @writer → all", other), "    writer · owner_agent", "    Unrelated.",
		thread[7:],
	)...)

	v := e.asAgent("reviewer", "read", "--thread", fmt.Sprint(yes), "--json").json(t)
	matchesCLISpec(t, "ReadThreadOutput", v)
	if got := field(t, v, "thread_root_seq"); got != float64(ask) {
		t.Fatalf("thread_root_seq = %v, want %d", got, ask)
	}
	if got := field(t, v, "root.reply_count"); got != float64(2) {
		t.Fatalf("root.reply_count = %v, want 2", got)
	}
	if got := field(t, v, "replies.1.thread_root_seq"); got != float64(ask) {
		t.Fatalf("a reply to a reply: thread_root_seq = %v, want %d", got, ask)
	}
	page := e.asAgent("reviewer", "read", "--json").json(t)
	matchesCLISpec(t, "ReadOutput", page)

	expectLines(t, e.asAgent("reviewer", "read", "--thread", fmt.Sprint(ask), "--markdown"),
		fmt.Sprintf("# writer-reviewer · #%d–#%d", ask, thanks),
		"",
		fmt.Sprintf("**#%d @writer** (writer, owner_agent) → @reviewer · asks for a reply · 2 replies", ask),
		"",
		"> Can you take the tests?",
		"",
		fmt.Sprintf("**#%d @reviewer** (reviewer, self) → @writer · reply to #%d", yes, ask),
		"",
		"> Yes. The build is broken first.",
		"",
		fmt.Sprintf("**#%d @writer** (writer, owner_agent) → @reviewer · reply to #%d", thanks, yes),
		"",
		"> Thanks.",
	)

	// A message without replies is a thread of its own.
	expectLines(t, e.asAgent("reviewer", "read", "--thread", fmt.Sprint(other)),
		fmt.Sprintf("writer-reviewer · thread #%d · no replies", other),
		fmt.Sprintf("#%d  @writer → all", other), "    writer · owner_agent", "    Unrelated.")

	// The API returns the same thread to the person.
	api := e.ownerRequest("GET", fmt.Sprintf("/v1/messages/%s/replies", field(t, v, "replies.1.id")), nil)
	if got := field(t, api, "root.seq"); got != float64(ask) || len(api["replies"].([]any)) != 2 {
		t.Fatalf("GET replies as the person: %v", api)
	}

	if r := e.runExit("read", "--as", "reviewer", "--thread", fmt.Sprint(ask), "--from", "@writer"); r.code != 2 ||
		!strings.Contains(r.stderr, "--thread") {
		t.Fatalf("--thread with a filter: want a usage error\n%s", r)
	}
	if r := e.runExit("read", "--as", "reviewer", "--thread", "99", "--json"); r.code != 1 ||
		field(t, r.json(t), "error.code") != "message_ref_invalid" {
		t.Fatalf("--thread with no such message:\n%s", r)
	}
}

// Under addressed visibility a thread shows an agent only the replies it may see, and
// a message it may not see is not found.
func TestReadThreadUnderAddressedVisibilityDoesNotLeak(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	threeAgents(t, e)
	e.run("board", "policy", "recommended")
	posted := field(t, e.run("say", "--as", "writer", "--to", "@reviewer", "only for the reviewer", "--json").json(t), "message").(map[string]any)
	ask := int(posted["seq"].(float64))
	yes := e.sayAs("reviewer", "--reply", fmt.Sprint(ask), "--to", "@writer", "for the writer")
	// The critic replies by id: it may not see the message, so it can't look up its number.
	mine := e.sayAs("critic", "--reply", posted["id"].(string), "--to", "@writer", "the critic's reply")

	expectLines(t, e.asAgent("reviewer", "read", "--thread", fmt.Sprint(ask)),
		fmt.Sprintf("writer-reviewer · thread #%d · 1 reply", ask),
		fmt.Sprintf("#%d  @writer → @reviewer · 1 reply", ask), "    writer · owner_agent", "    only for the reviewer",
		fmt.Sprintf("#%d  @reviewer → @writer · reply to #%d", yes, ask), "    reviewer · self", "    for the writer")

	// The critic may see its own reply, not the message it answers.
	expectLines(t, e.asAgent("critic", "read", "--thread", fmt.Sprint(mine)),
		fmt.Sprintf("writer-reviewer · thread #%d · 1 reply", ask),
		fmt.Sprintf("#%d  @critic → @writer · reply to #%d", mine, ask), "    reviewer · self", "    the critic's reply")
	if r := e.runExit("read", "--as", "critic", "--thread", fmt.Sprint(ask), "--json"); r.code != 1 ||
		field(t, r.json(t), "error.code") != "message_ref_invalid" {
		t.Fatalf("the critic reading a thread from a message it may not see:\n%s", r)
	}
}
