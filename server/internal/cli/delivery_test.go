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

// The formatter itself is tested in deliverytext; this checks the API message reaches it
// with every field the delivery text shows.
func TestDeliveryTextCarriesTheAPIMessage(t *testing.T) {
	m := agentMessage()
	m.Urgent, m.ExpectsReply, m.ReplyToSeq = true, true, ptr(4)
	want := "<aboard-message board=\"writer-reviewer\" from=\"@writer\" owner=\"alex\" role=\"writer\" trust=\"peer\" seq=\"6\" urgent=\"true\" expects-reply=\"true\" reply-to=\"4\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
		"Reply requested. Reply with: aboard say --reply 6 \"…\""
	if got := deliveryText(m); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	human := agentMessage()
	human.From = api.MemberRef{Kind: "human", Name: "alex", Owner: ptr("alex")}
	human.Trust = "owner"
	want = "<aboard-message board=\"writer-reviewer\" from=\"@alex\" owner=\"\" role=\"\" trust=\"owner\" seq=\"6\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>"
	if got := deliveryText(human); got != want {
		t.Errorf("human sender:\ngot:\n%s\nwant:\n%s", got, want)
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
