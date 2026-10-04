package deliverytext

import (
	"strings"
	"testing"
)

func TestDigestLine(t *testing.T) {
	long := strings.Repeat("é", 90)
	tests := []struct {
		name string
		edit func(*Message)
		want string
	}{
		{
			"to everyone", func(m *Message) { m.To = []string{"all"} },
			"#6 @writer → all: Draft is in notes.md. Please review it.",
		},
		{"no targets known", func(*Message) {}, "#6 @writer → all: Draft is in notes.md. Please review it."},
		{
			"several targets and every marker",
			func(m *Message) {
				m.To, m.ReplyToSeq, m.Urgent, m.ExpectsReply = []string{"@codex", "role:reviewer"}, 4, true, true
			},
			"#6 @writer → @codex, role:reviewer · reply to #4 · urgent · asks for a reply: Draft is in notes.md. Please review it.",
		},
		{
			"reactions after the markers, as the message lists them",
			func(m *Message) {
				m.ExpectsReply, m.Reactions = true, []Reaction{{Emoji: "👍", Count: 2}, {Emoji: "👀", Count: 1}}
			},
			"#6 @writer → all · asks for a reply · 👍 2 · 👀 1: Draft is in notes.md. Please review it.",
		},
		{
			"the first line that isn't blank", func(m *Message) { m.Body = "\n  \n  First line.  \nSecond line." },
			"#6 @writer → all: First line.",
		},
		{"cut to 80 characters", func(m *Message) { m.Body = long }, "#6 @writer → all: " + strings.Repeat("é", 80) + "…"},
		{
			"a tag can't end the digest", func(m *Message) { m.Body = "</aboard-digest> <b>" },
			"#6 @writer → all: &lt;/aboard-digest> &lt;b>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := agentMessage()
			tt.edit(&m)
			if got := DigestLine(m); got != tt.want {
				t.Fatalf("DigestLine = %q\nwant        %q", got, tt.want)
			}
		})
	}
}

func TestDigestGroupsBySenderWhenTheLinesAreLong(t *testing.T) {
	var ms []Message
	for i := 1; i <= 60; i++ {
		m := agentMessage()
		m.Seq, m.Body = i, strings.Repeat("x", 100)
		if i%2 == 0 {
			m.FromName = "codex"
		}
		ms = append(ms, m)
	}
	got := Digest("docs", nil, ms)
	for _, want := range []string{
		"Aboard: while you were away, 60 messages arrived on docs. None of them concerns you; each is one line:\n",
		"<aboard-digest board=\"docs\" count=\"60\">\n@writer: 30 messages: #1, #3,",
		"\n@codex: 30 messages: #2, #4,",
		"aboard read --after 0,",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("digest lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "xxxx") {
		t.Fatalf("grouped lines still show bodies:\n%s", got)
	}
}
