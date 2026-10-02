package cli

import (
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// textMessage turns an API message into what the delivery text shows of it.
func textMessage(m api.Message) deliverytext.Message { return apiserver.TextMessage(m) }

// deliveryText formats one message the way it is put into a session.
func deliveryText(m api.Message) string { return deliverytext.Format(textMessage(m)) }

// bundleText formats several messages of one board delivered together, oldest first.
func bundleText(board string, ms []api.Message) string {
	tms := make([]deliverytext.Message, 0, len(ms))
	for _, m := range ms {
		tms = append(tms, textMessage(m))
	}
	return deliverytext.Bundle(board, tms)
}

// transcriptText formats messages as a Markdown transcript to paste into a session.
// Each body is a blockquote, so text in a message can't pass for another message's
// heading.
func transcriptText(board string, ms []api.Message) string {
	var b strings.Builder
	switch len(ms) {
	case 0:
		return "# " + board + " · no messages\n"
	case 1:
		fmt.Fprintf(&b, "# %s · #%d\n", board, ms[0].Seq)
	default:
		fmt.Fprintf(&b, "# %s · #%d–#%d\n", board, ms[0].Seq, ms[len(ms)-1].Seq)
	}
	for _, m := range ms {
		fmt.Fprintf(&b, "\n**#%d @%s**%s → %s", m.Seq, m.From.Name, senderText(m), targetsText(m.To))
		if m.ReplyToSeq != nil {
			fmt.Fprintf(&b, " · reply to #%d", *m.ReplyToSeq)
		}
		b.WriteString("\n\n")
		for _, line := range strings.Split(strings.TrimSuffix(m.Body, "\n"), "\n") {
			if line == "" {
				b.WriteString(">\n")
			} else {
				b.WriteString("> " + line + "\n")
			}
		}
	}
	return b.String()
}

// senderText describes a message's sender after its name: the owner once the board
// has agents of more than one person, then in brackets an agent's role and harness, and
// the sender label.
func senderText(m api.Message) string {
	var owner string
	if m.ShowOwner && m.From.Kind == "agent" && m.From.Owner != nil {
		owner = " · " + *m.From.Owner
	}
	var parts []string
	if m.From.Kind == "agent" {
		parts = append(parts, deref(m.From.Role))
		if h := deref(m.From.Harness); h != "" {
			parts = append(parts, h)
		}
	}
	parts = append(parts, string(m.Sender))
	return owner + " (" + strings.Join(parts, ", ") + ")"
}

// timelineText formats messages for reading the board: a header line per message and
// its body indented below it.
func timelineText(ms []api.Message) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "#%d  @%s%s → %s\n", m.Seq, m.From.Name, senderText(m), targetsText(m.To))
		for _, line := range strings.Split(strings.TrimSuffix(m.Body, "\n"), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}
