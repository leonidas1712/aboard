package delivery

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func msg(board string, seq int, urgent bool, body string) Message {
	return Message{Board: board, FromName: "writer", Role: "writer", Sender: "owner_agent", Seq: seq, Urgent: urgent, Body: body}
}

func TestOrderForBundlePutsUrgentFirstThenOldest(t *testing.T) {
	ms := []Message{msg("docs", 9, false, ""), msg("docs", 7, true, ""), msg("docs", 6, false, ""), msg("docs", 8, true, "")}
	orderForBundle(ms)
	var got []int
	for _, m := range ms {
		got = append(got, m.Seq)
	}
	if want := []int{7, 8, 6, 9}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

func TestCompose(t *testing.T) {
	docs := AgentRef{Board: "docs", Name: "reviewer"}
	plans := AgentRef{Board: "plans", Name: "planner"}
	big := strings.Repeat("x", 12<<10)
	tests := []struct {
		name         string
		offers       []offer
		wantSeqs     [][]int
		wantTooLarge []int
	}{
		{
			"everything fits",
			[]offer{{agent: docs, msgs: []Message{msg("docs", 1, false, "a"), msg("docs", 2, false, "b")}}},
			[][]int{{1, 2}},
			nil,
		},
		{
			"what doesn't fit waits",
			[]offer{{agent: docs, msgs: []Message{msg("docs", 1, false, big), msg("docs", 2, false, big), msg("docs", 3, false, big)}}},
			[][]int{{1, 2}},
			nil,
		},
		{
			"a message too large for any bundle is set aside",
			[]offer{{agent: docs, msgs: []Message{msg("docs", 1, false, big+big+big), msg("docs", 2, false, "b")}}},
			[][]int{{2}},
			[]int{1},
		},
		{"a redelivery is all or nothing", []offer{
			{agent: docs, msgs: []Message{msg("docs", 1, false, big), msg("docs", 2, false, big)}},
			{agent: plans, redeliver: 7, msgs: []Message{msg("plans", 1, false, big)}},
		}, [][]int{{1, 2}}, nil},
		{"two boards", []offer{
			{agent: docs, msgs: []Message{msg("docs", 4, false, "a")}},
			{agent: plans, msgs: []Message{msg("plans", 2, false, "b")}},
		}, [][]int{{4}, {2}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := compose(tt.offers, BundleLimit)
			if len(c.text) > BundleLimit {
				t.Fatalf("bundle of %d bytes", len(c.text))
			}
			if len(c.parts) != len(tt.wantSeqs) {
				t.Fatalf("parts %d, want %d", len(c.parts), len(tt.wantSeqs))
			}
			for i, p := range c.parts {
				if got := seqsOf(p.msgs); !slices.Equal(got, tt.wantSeqs[i]) {
					t.Errorf("part %d: seqs %v, want %v", i, got, tt.wantSeqs[i])
				}
			}
			var tooLarge []int
			for _, p := range c.tooLarge {
				tooLarge = append(tooLarge, seqsOf(p.msgs)...)
			}
			if !slices.Equal(tooLarge, tt.wantTooLarge) {
				t.Errorf("too large %v, want %v", tooLarge, tt.wantTooLarge)
			}
		})
	}
}

func TestConcerns(t *testing.T) {
	base := Message{FromName: "codex", Sender: "owner_agent", To: []string{"all"}}
	tests := []struct {
		name string
		edit func(*Message)
		want bool
	}{
		{"another agent's message to everyone", func(*Message) {}, false},
		{"from a person", func(m *Message) { m.FromHuman = true }, true},
		{"to the agent by name", func(m *Message) { m.To = []string{"@reviewer"} }, true},
		{"to its role", func(m *Message) { m.To = []string{"role:reviewer"} }, true},
		{"a reply to its message", func(m *Message) { m.ReplyToSeq, m.ReplyToFrom = 3, "reviewer" }, true},
		{"a reply to someone else's", func(m *Message) { m.ReplyToSeq, m.ReplyToFrom = 3, "omp" }, false},
		{"a question to everyone", func(m *Message) { m.ExpectsReply = true }, true},
		{"urgent", func(m *Message) { m.Urgent = true }, true},
		{"targets not known", func(m *Message) { m.To = nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base
			tt.edit(&m)
			if got := Concerns(m, "reviewer"); got != tt.want {
				t.Fatalf("Concerns = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComposeInFocusedMode(t *testing.T) {
	docs := AgentRef{Board: "docs", Name: "reviewer"}
	quiet := func(seq int) Message {
		m := msg("docs", seq, false, "update "+strings.Repeat("x", 30))
		m.To = []string{"all"}
		return m
	}
	direct := msg("docs", 20, false, "please review")
	direct.To = []string{"@reviewer"}

	c := compose([]offer{{agent: docs, mode: ModeFocused, msgs: []Message{quiet(1), direct}}}, BundleLimit)
	at, away := strings.Index(c.text, "please review"), strings.Index(c.text, "while you were away")
	if len(c.parts) != 1 || at < 0 || away < at || !strings.Contains(c.text, `quiet="true"`) {
		t.Fatalf("a small focused bundle: the message to the agent, then the quiet one apart:\n%s", c.text)
	}

	var many []Message
	for seq := 1; seq <= 11; seq++ {
		many = append(many, quiet(seq))
	}
	c = compose([]offer{{agent: docs, mode: ModeFocused, msgs: append(many, direct)}}, BundleLimit)
	if got := seqsOf(c.parts[0].msgs); len(got) != 12 {
		t.Fatalf("a digest carries all 12 messages, got %v", got)
	}
	if !strings.Contains(c.text, `<aboard-digest board="docs" count="11">`) || strings.Count(c.text, "<aboard-message ") != 1 {
		t.Fatalf("past 10 messages, only the one that concerns the agent is in full:\n%s", c.text)
	}

	c = compose([]offer{{agent: docs, mode: ModeAll, msgs: append(many, direct)}}, BundleLimit)
	if strings.Contains(c.text, "aboard-digest") || strings.Count(c.text, "<aboard-message ") != 12 {
		t.Fatalf("all mode never summarizes:\n%s", c.text)
	}
}

func TestBackoffDoublesUpToAMinute(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, w)
		}
	}
}

func TestFramesOverTheLimitAreRefused(t *testing.T) {
	var b strings.Builder
	if err := WriteFrame(&b, Request{V: 1, Op: OpStatus, Session: strings.Repeat("x", MaxFrame)}); err == nil {
		t.Fatal("an oversized frame was written")
	}
	if err := WriteFrame(&b, Response{V: 1, Bundle: strings.Repeat("<", BundleLimit)}); err != nil {
		t.Fatalf("a full bundle of \"<\" must fit in a frame: %v", err)
	}
}

func TestMultiIssuerDefaultCompositionAndFrozenRenderAgree(t *testing.T) {
	offers := []offer{
		{agent: AgentRef{Server: "https://one.example", Board: "docs", Name: "reviewer", MemberID: "mem_same"}, msgs: []Message{msg("docs", 1, false, "first")}},
		{agent: AgentRef{Server: "https://two.example", Board: "docs", Name: "reviewer", MemberID: "mem_same"}, msgs: []Message{msg("docs", 1, false, "second")}},
	}
	c := compose(offers, 32<<10)
	if len(c.parts) != 2 {
		t.Fatalf("issuer seats merged: %+v", c.parts)
	}
	for _, issuer := range []string{"https://one.example", "https://two.example"} {
		if !strings.Contains(c.text, `server="`+issuer+`"`) || !strings.Contains(c.text, `seat="reviewer"`) {
			t.Fatalf("default composition lacks issuer/seat: %s", c.text)
		}
	}
	if frozen := renderComposition(c.parts, false, c.digests, c.renderedAt); frozen != c.text {
		t.Fatalf("frozen render changed payload:\n%s\n%s", c.text, frozen)
	}
}
