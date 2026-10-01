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
