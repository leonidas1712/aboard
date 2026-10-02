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
