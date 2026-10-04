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
		fmt.Fprintf(&b, "\n**#%d @%s**%s → %s%s\n\n", m.Seq, m.From.Name, senderText(m), targetsText(m.To), markersText(m))
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

// senderText describes a message's sender after its name in a Markdown transcript: the
// owner once the board has agents of more than one person, then in brackets an agent's
// role and harness, and the sender label.
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

// markersText lists what a message is besides its text, as the board view marks it:
// what it replies to, whether it is urgent, whether it asks for a reply, how many
// replies the thread it starts has, and its reactions.
func markersText(m api.Message) string {
	var b strings.Builder
	if m.ReplyToSeq != nil {
		fmt.Fprintf(&b, " · reply to #%d", *m.ReplyToSeq)
	}
	if m.Urgent {
		b.WriteString(" · urgent")
	}
	if m.ExpectsReply {
		b.WriteString(" · asks for a reply")
	}
	if m.ReplyCount > 0 {
		b.WriteString(" · " + repliesText(m.ReplyCount, ""))
	}
	b.WriteString(reactionsText(m))
	return b.String()
}

// senderLine describes who sent a message, on a line of its own: an agent's role,
// harness and owner (when the board shows it), then the sender label.
func senderLine(m api.Message) string {
	var parts []string
	if m.From.Kind == "agent" {
		parts = append(parts, deref(m.From.Role))
		if h := deref(m.From.Harness); h != "" {
			parts = append(parts, h)
		}
		if m.ShowOwner && m.From.Owner != nil {
			parts = append(parts, "owner "+*m.From.Owner)
		}
	}
	return strings.Join(append(parts, string(m.Sender)), " · ")
}

// timelineText formats messages for reading the board: per message, a line with its
// sender, targets and markers, a line describing the sender, and the body, all but the
// first line indented.
func timelineText(ms []api.Message) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "#%d  @%s → %s%s\n", m.Seq, m.From.Name, targetsText(m.To), markersText(m))
		b.WriteString("    " + senderLine(m) + "\n")
		for _, line := range strings.Split(strings.TrimSuffix(m.Body, "\n"), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}
