package cli

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func ptr[T any](v T) *T { return &v }

func agentMessage() api.Message {
	return api.Message{
		Board: "writer-reviewer", Body: "Draft is in notes.md. Please review it.", Seq: 6, Sender: "owner_agent", Trust: "peer",
		From: api.MemberRef{Kind: "agent", Name: "writer", Owner: ptr("alex"), Role: ptr("writer")},
		To:   []string{"@reviewer"},
	}
}

// The formatter itself is tested in deliverytext; this checks the API message reaches it
// with every field the delivery text shows.
func TestDeliveryTextCarriesTheAPIMessage(t *testing.T) {
	m := agentMessage()
	m.Urgent, m.ExpectsReply, m.ReplyToSeq = true, true, ptr(4)
	want := "<aboard-message board=\"writer-reviewer\" from=\"@writer\" role=\"writer\" sender=\"owner_agent\" seq=\"6\" urgent=\"true\" expects-reply=\"true\" reply-to=\"4\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>\n" +
		"Reply requested. Reply with: aboard say --reply 6 \"…\""
	if got := deliveryText(m); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	other := agentMessage()
	other.From = api.MemberRef{Kind: "agent", Name: "codex", Owner: ptr("priya"), Role: ptr("reviewer"), Harness: ptr("codex")}
	other.Sender, other.ShowOwner = "other_agent", true
	want = "<aboard-message board=\"writer-reviewer\" from=\"@codex\" owner=\"priya\" role=\"reviewer\" harness=\"codex\" sender=\"other_agent\" seq=\"6\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>"
	if got := deliveryText(other); got != want {
		t.Errorf("another owner's agent:\ngot:\n%s\nwant:\n%s", got, want)
	}
	human := agentMessage()
	human.From = api.MemberRef{Kind: "human", Name: "alex"}
	human.Sender, human.ShowOwner = "owner", true
	want = "<aboard-message board=\"writer-reviewer\" from=\"@alex\" sender=\"owner\" seq=\"6\">\n" +
		"Draft is in notes.md. Please review it.\n</aboard-message>"
	if got := deliveryText(human); got != want {
		t.Errorf("human sender:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestTimelineText(t *testing.T) {
	agent := agentMessage()
	agent.Body, agent.Sender, agent.ExpectsReply = "two\nlines", "self", true
	human := agentMessage()
	human.Seq, human.Body, human.To, human.Sender = 7, "Approved.", nil, "owner"
	human.From = api.MemberRef{Kind: "human", Name: "alex"}
	other := agentMessage()
	other.Seq, other.Body, other.Sender, other.ShowOwner = 8, "Seen.", "other_agent", true
	other.ReplyToSeq, other.Urgent, other.ExpectsReply = ptr(6), true, true
	other.From = api.MemberRef{Kind: "agent", Name: "codex", Owner: ptr("priya"), Role: ptr("reviewer"), Harness: ptr("codex")}
	want := "#6  @writer → @reviewer · asks for a reply\n    writer · self\n    two\n    lines\n" +
		"#7  @alex → all\n    owner\n    Approved.\n" +
		"#8  @codex → @reviewer · reply to #6 · urgent · asks for a reply\n" +
		"    reviewer · codex · owner priya · other_agent\n    Seen.\n"
	if got := timelineText([]api.Message{agent, human, other}); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestShortHash(t *testing.T) {
	h := "sha256:3f9a0c1e0000000000000000000000000000000000000000000000000000abcd"
	if got, want := shortHash(h), "sha256:3f9a0c1e…"; got != want {
		t.Errorf("shortHash = %q, want %q", got, want)
	}
}
