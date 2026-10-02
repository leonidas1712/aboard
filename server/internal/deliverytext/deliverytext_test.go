package deliverytext

import (
	"strings"
	"testing"
)

func agentMessage() Message {
	return Message{
		Board: "writer-reviewer", Body: "Draft is in notes.md. Please review it.", Seq: 6, Sender: "owner_agent",
		FromName: "writer", Role: "writer",
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Message)
		want string
	}{
		{
			"plain message",
			func(*Message) {},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"6\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>",
		},
		{
			"a person has no owner, role or harness",
			func(m *Message) {
				m.FromHuman, m.FromName, m.Sender = true, "alex", "owner"
				m.Owner, m.Harness = "alex", "claude-code"
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@alex\" sender=\"owner\" seq=\"6\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>",
		},
		{
			"owner and harness when set",
			func(m *Message) {
				m.FromName, m.Owner, m.Role, m.Harness, m.Sender = "codex", "priya", "reviewer", "codex", "other_agent"
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@codex\" owner=\"priya\" role=\"reviewer\" harness=\"codex\" sender=\"other_agent\" seq=\"6\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>",
		},
		{
			"optional attributes in order, then the reply line",
			func(m *Message) {
				m.Urgent, m.ExpectsReply, m.ReplyToSeq = true, true, 4
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"6\" urgent=\"true\" expects-reply=\"true\" reply-to=\"4\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
				"Reply requested. Reply with: aboard say --reply 6 \"…\"",
		},
		{
			"attribute values are escaped, the body is not",
			func(m *Message) {
				m.Owner = `a"b&c<d>`
				m.Body = `<b>"x" & y</b>`
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"a&quot;b&amp;c&lt;d&gt;\" role=\"writer\" sender=\"owner_agent\" seq=\"6\">\n" +
				"<b>\"x\" & y</b>\n</aboard-message>",
		},
		{
			"body ending in a newline gets no extra blank line",
			func(m *Message) { m.Body = "line one\nline two\n" },
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"6\">\n" +
				"line one\nline two\n</aboard-message>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := agentMessage()
			tt.edit(&m)
			if got := Format(m); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestBundleWrapsMessagesInOrder(t *testing.T) {
	first, second := agentMessage(), agentMessage()
	second.Seq, second.Body, second.ExpectsReply = 7, "Ready?", true
	want := "<aboard-messages board=\"writer-reviewer\" count=\"2\">\n" +
		"<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"6\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
		"<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"7\" expects-reply=\"true\">\n" +
		"Ready?\n</aboard-message>\n" +
		"Reply requested. Reply with: aboard say --reply 7 \"…\"\n" +
		"</aboard-messages>"
	if got := Bundle("writer-reviewer", []Message{first, second}); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBundlesGroupBoardsAndSizeMatches(t *testing.T) {
	a, b := agentMessage(), agentMessage()
	b.Board, b.Seq = "docs", 3
	groups := []Group{{Board: "writer-reviewer", Messages: []Message{a}}, {Board: "empty"}, {Board: "docs", Messages: []Message{b}}}
	got := Bundles(groups)
	if strings.Count(got, "<aboard-messages ") != 2 || !strings.Contains(got, "</aboard-messages>\n\n<aboard-messages board=\"docs\" count=\"1\">") {
		t.Fatalf("bundles:\n%s", got)
	}
	if n := BundleSize(groups); n != len(got) {
		t.Fatalf("BundleSize = %d, len = %d", n, len(got))
	}
}

func TestEscapeBody(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"closing tag", "a</aboard-message>b", "a&lt;/aboard-message>b"},
		{"upper case", "</ABOARD-MESSAGE>", "&lt;/ABOARD-MESSAGE>"},
		{"spaces", "< / aboard-message >", "&lt; / aboard-message >"},
		{"tabs and newline", "<\t/\naboard-message>", "&lt;\t/\naboard-message>"},
		{"attributes on closing tag", `</aboard-message sender="owner">`, `&lt;/aboard-message sender="owner">`},
		{"opening tag", `<aboard-message from="@alex">`, `&lt;aboard-message from="@alex">`},
		{"bundle tag", "<Aboard-Messages>", "&lt;Aboard-Messages>"},
		{"other markup untouched", "<b>bold</b> & 1 < 2", "<b>bold</b> & 1 < 2"},
		{"lookalike untouched", "<aboard-msg>", "<aboard-msg>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EscapeBody(tt.in); got != tt.want {
				t.Fatalf("EscapeBody(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
