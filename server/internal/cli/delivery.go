package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// attrEscaper escapes text for an attribute value in delivery text.
var attrEscaper = strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")

// deliveryText formats one message the way it is put into a session: the sender's text
// unchanged between Aboard's tags, and a reply instruction when a reply is expected.
func deliveryText(m api.Message) string {
	owner, role := deref(m.From.Owner), deref(m.From.Role)
	if m.From.Kind == "human" {
		owner, role = "", ""
	}
	attrs := [][2]string{
		{"board", m.Board},
		{"from", "@" + m.From.Name},
		{"owner", owner},
		{"role", role},
		{"trust", string(m.Trust)},
		{"seq", strconv.Itoa(m.Seq)},
	}
	if m.Urgent {
		attrs = append(attrs, [2]string{"urgent", "true"})
	}
	if m.ExpectsReply {
		attrs = append(attrs, [2]string{"expects-reply", "true"})
	}
	if m.ReplyToSeq != nil {
		attrs = append(attrs, [2]string{"reply-to", strconv.Itoa(*m.ReplyToSeq)})
	}
	var b strings.Builder
	b.WriteString("<aboard-message")
	for _, kv := range attrs {
		fmt.Fprintf(&b, ` %s="%s"`, kv[0], attrEscaper.Replace(kv[1]))
	}
	b.WriteString(">\n")
	b.WriteString(m.Body)
	if !strings.HasSuffix(m.Body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("</aboard-message>")
	if m.ExpectsReply {
		fmt.Fprintf(&b, "\nReply requested. Reply with: aboard say --reply %d \"…\"", m.Seq)
	}
	return b.String()
}

// bundleText formats several messages delivered together, oldest first.
func bundleText(board string, ms []api.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<aboard-messages board=\"%s\" count=\"%d\">\n", attrEscaper.Replace(board), len(ms))
	for _, m := range ms {
		b.WriteString(deliveryText(m) + "\n")
	}
	b.WriteString("</aboard-messages>")
	return b.String()
}

// timelineText formats messages for reading the board: a header line per message and
// its body indented below it.
func timelineText(ms []api.Message) string {
	var b strings.Builder
	for _, m := range ms {
		who := "(human)"
		if m.From.Kind != "human" {
			who = fmt.Sprintf("(%s, %s)", deref(m.From.Role), deref(m.From.Owner))
		}
		fmt.Fprintf(&b, "#%d  @%s %s → %s\n", m.Seq, m.From.Name, who, targetsText(m.To))
		for _, line := range strings.Split(strings.TrimSuffix(m.Body, "\n"), "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}
