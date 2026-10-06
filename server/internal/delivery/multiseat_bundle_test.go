package delivery

import (
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

func TestMessageThatFitsWholeBundleWaitsForAnotherBoardsSpace(t *testing.T) {
	a := AgentRef{Server: "server", MemberID: "mem_a", Board: "a", Name: "writer"}
	b := AgentRef{Server: "server", MemberID: "mem_b", Board: "b", Name: "reviewer"}
	offers := []offer{{agent: a, msgs: []Message{msg("a", 1, false, strings.Repeat("a", 300))}}, {agent: b, msgs: []Message{msg("b", 1, false, strings.Repeat("b", 300))}}}
	limit := max(len(compose(offers[:1], BundleLimit).text), len(compose(offers[1:], BundleLimit).text)) + 20
	got := compose(offers, limit)
	if len(got.tooLarge) != 0 {
		t.Fatalf("fitting later-board message marked oversized: %+v", got.tooLarge)
	}
	if len(got.parts) != 1 || got.parts[0].agent.Key() != a.Key() {
		t.Fatalf("first board should fit and second should wait: %+v", got.parts)
	}
}

func TestCompositionGroupsOneSeatThroughItsDisplayRename(t *testing.T) {
	old := AgentRef{Server: "server", MemberID: "mem_same", Board: "a", Name: "old"}
	renamed := old
	renamed.Name = "renamed"
	got := compose([]offer{{agent: old, msgs: []Message{msg("a", 1, false, "before")}}, {agent: renamed, msgs: []Message{msg("a", 2, false, "after")}}}, BundleLimit)
	if n := strings.Count(got.text, "<aboard-messages "); n != 1 {
		t.Fatalf("one immutable seat rendered %d blocks:\n%s", n, got.text)
	}
}

func TestSharedBudgetCountsRoutingAndReservedNotes(t *testing.T) {
	a := AgentRef{Server: "server", MemberID: "mem_a", Board: "a", Name: "writer"}
	m := msg("a", 1, false, strings.Repeat("é", 80))
	m.ExpectsReply = true
	offers := []offer{{agent: a, msgs: []Message{m}}}
	full := compose(offers, BundleLimit, composeOptions{MultiSeat: true})
	limit := len(full.text)
	got := compose(offers, limit-1, composeOptions{MultiSeat: true, WholeLimit: limit})
	if len(got.parts) != 0 || len(got.tooLarge) != 0 {
		t.Fatalf("routing or reserved-note space wasn't deferred: %+v", got)
	}
	got = compose(offers, limit, composeOptions{MultiSeat: true})
	if got.text != full.text || len(got.text) > limit {
		t.Fatalf("exact byte budget did not fit:\n%s", got.text)
	}
	if !strings.Contains(got.text, `seat="writer"`) || !strings.Contains(got.text, "aboard say --board a --reply 1") {
		t.Fatalf("missing routing context:\n%s", got.text)
	}
}

func TestRotatingFirstSeatDoesNotStarveAnotherBoard(t *testing.T) {
	var offers []offer
	for _, board := range []string{"a", "b", "c"} {
		offers = append(offers, offer{agent: AgentRef{Server: "server", MemberID: "mem_" + board, Board: board, Name: "worker"}, msgs: []Message{msg(board, 1, false, strings.Repeat("x", 300))}})
	}
	limit := len(compose(offers[:1], BundleLimit, composeOptions{MultiSeat: true}).text) + 1
	first := 0
	for _, want := range []string{"a", "b", "c", "a"} {
		c := compose(offers, limit, composeOptions{MultiSeat: true, First: first})
		if len(c.parts) != 1 || c.parts[0].agent.Board != want || len(c.tooLarge) != 0 {
			t.Fatalf("rotation %d wanted %s: %+v", first, want, c)
		}
		first = c.nextFirst
	}
}

func TestRetryKeepsTheAdmittedDigestBelowTheBacklogThreshold(t *testing.T) {
	agent := AgentRef{Server: "server", MemberID: "mem_a", Board: "a", Name: "reviewer"}
	var quiet, concern []Message
	for seq := 1; seq <= 2; seq++ {
		m := msg("a", seq, false, "quiet")
		m.To = []string{"all"}
		quiet = append(quiet, m)
	}
	for seq := 3; seq <= 12; seq++ {
		m := msg("a", seq, false, strings.Repeat("x", 300))
		m.To = []string{"@reviewer"}
		concern = append(concern, m)
	}
	context := deliverytext.Context{Seat: agent.Name, BoardQualified: true}
	limit := len(deliverytext.Digest("a", concern[:1], quiet, context))
	c := compose([]offer{{agent: agent, mode: ModeFocused, msgs: append(quiet, concern...)}}, limit, composeOptions{MultiSeat: true})
	if len(c.parts) != 1 || len(c.parts[0].msgs) >= DigestMessages || !c.digests[agent.Key()] {
		t.Fatalf("did not admit a below-threshold digest: %+v", c)
	}
	if got := renderComposition(c.parts, true, c.digests); got != c.text {
		t.Fatalf("retry changed admitted text:\n%s\nwant:\n%s", got, c.text)
	}
	if len(c.text) > limit {
		t.Fatalf("digest exceeded limit: %d > %d", len(c.text), limit)
	}
}

func TestSingleSeatKeepsItsConservativeFocusedAdmission(t *testing.T) {
	agent := AgentRef{Server: "server", MemberID: "mem_a", Board: "a", Name: "reviewer"}
	var messages []Message
	for seq := 1; seq <= 2; seq++ {
		m := msg("a", seq, false, strings.Repeat("x", 100))
		m.To = []string{"@reviewer"}
		messages = append(messages, m)
	}
	limit := deliverytext.BundleSize([]deliverytext.Group{{Board: "a", Messages: messages}}) + 399
	legacy := compose([]offer{{agent: agent, mode: ModeFocused, msgs: messages}}, limit)
	if len(legacy.parts) != 1 || len(legacy.parts[0].msgs) != 1 || legacy.parts[0].msgs[0].Seq != 1 {
		t.Fatalf("single-seat admission changed: %+v", legacy.parts)
	}
	if want := deliverytext.Woken("a", messages[:1], nil); legacy.text != want {
		t.Fatalf("single-seat text changed:\n%s\nwant:\n%s", legacy.text, want)
	}
	several := compose([]offer{{agent: agent, mode: ModeFocused, msgs: messages}}, limit, composeOptions{MultiSeat: true})
	if len(several.parts) != 1 || len(several.parts[0].msgs) != 2 || len(several.text) > limit {
		t.Fatalf("multi-seat composition didn't use actual budget: %+v", several)
	}
}
