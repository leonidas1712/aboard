package cli

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func ptr[T any](v T) *T { return &v }

func agentMessage() api.Message {
	return api.Message{
		Board: "writer-reviewer", Body: "Draft is in notes.md. Please review it.", Seq: 6, Trust: "peer",
		From: api.MemberRef{Kind: "agent", Name: "writer", Owner: ptr("alex"), Role: ptr("writer")},
		To:   []string{"@reviewer"},
	}
}

func TestDeliveryText(t *testing.T) {
	tests := []struct {
		name string
		edit func(*api.Message)
		want string
	}{
		{
			"plain message",
			func(*api.Message) {},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>",
		},
		{
			"human sender has empty owner and role",
			func(m *api.Message) {
				m.From = api.MemberRef{Kind: "human", Name: "alex", Owner: ptr("alex")}
				m.Trust = "owner"
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@alex\" owner=\"\" role=\"\" trust=\"owner\" seq=\"6\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>",
		},
		{
			"optional attributes in order, then the reply line",
			func(m *api.Message) {
				m.Urgent, m.ExpectsReply, m.ReplyToSeq = true, true, ptr(4)
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\" urgent=\"true\" expects-reply=\"true\" reply-to=\"4\">\n" +
				"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
				"Reply requested. Reply with: aboard say --reply 6 \"…\"",
		},
		{
			"attribute values are escaped, the body is not",
			func(m *api.Message) {
				m.From.Owner = ptr(`a"b&c<d>`)
				m.Body = `<b>"x" & y</b>`
			},
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"a&quot;b&amp;c&lt;d&gt;\" role=\"writer\" trust=\"peer\" seq=\"6\">\n" +
				"<b>\"x\" & y</b>\n</aboard-message>",
		},
		{
			"body ending in a newline gets no extra blank line",
			func(m *api.Message) { m.Body = "line one\nline two\n" },
			"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\">\n" +
				"line one\nline two\n</aboard-message>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := agentMessage()
			tt.edit(&m)
			if got := deliveryText(m); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestBundleTextWrapsMessagesInOrder(t *testing.T) {
	first, second := agentMessage(), agentMessage()
	second.Seq, second.Body, second.ExpectsReply = 7, "Ready?", true
	want := "<aboard-messages board=\"writer-reviewer\" count=\"2\">\n" +
		"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
		"<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"7\" expects-reply=\"true\">\n" +
		"Ready?\n</aboard-message>\n" +
		"Reply requested. Reply with: aboard say --reply 7 \"…\"\n" +
		"</aboard-messages>"
	if got := bundleText("writer-reviewer", []api.Message{first, second}); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTimelineText(t *testing.T) {
	agent := agentMessage()
	agent.Body = "two\nlines"
	human := agentMessage()
	human.Seq, human.Body, human.To = 7, "Approved.", nil
	human.From = api.MemberRef{Kind: "human", Name: "alex"}
	want := "#6  @writer (writer, alex) → @reviewer\n    two\n    lines\n" +
		"#7  @alex (human) → all\n    Approved.\n"
	if got := timelineText([]api.Message{agent, human}); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestShortHash(t *testing.T) {
	h := "sha256:3f9a0c1e0000000000000000000000000000000000000000000000000000abcd"
	if got, want := shortHash(h), "sha256:3f9a0c1e…"; got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}
