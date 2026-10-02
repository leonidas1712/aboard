// Package deliverytext writes board messages in the text form that is put into an agent
// session: each message in an <aboard-message> element, several in one <aboard-messages>
// element. The CLI's inbox and the delivery daemon both use it, so an agent sees the same
// text however a message reaches it.
package deliverytext

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Message is what the delivery text shows of one board message.
type Message struct {
	Board string
	// FromName is the sender's member name, without the "@".
	FromName string
	// FromHuman is true when a person sent the message; owner, role and harness are
	// then left out.
	FromHuman bool
	// Owner is the sending agent's owner, or empty when the board has agents of only one
	// person, so naming the owner adds nothing.
	Owner string
	Role  string
	// Harness is the sending agent's harness, or empty when unknown or hidden.
	Harness string
	// Sender is the sender label: owner, owner_agent, other_person or other_agent.
	Sender       string
	Seq          int
	Urgent       bool
	ExpectsReply bool
	// ReplyToSeq is the sequence number of the message this one replies to, or 0.
	ReplyToSeq int
	Body       string
}

// attrEscaper escapes text for an attribute value.
var attrEscaper = strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")

// tagLike matches the start of anything that reads as an aboard-message or
// aboard-messages tag: "<", optional whitespace, an optional "/", optional whitespace,
// then the name, in any case.
var tagLike = regexp.MustCompile(`(?i)<(\s*/?\s*aboard-message)`)

// EscapeBody stops a message body from ending its <aboard-message> element early or
// opening a fake one, by writing the "<" of any tag-like text as "&lt;". Nothing else in
// the body changes.
func EscapeBody(body string) string {
	return tagLike.ReplaceAllString(body, "&lt;$1")
}

// Format writes one message: the sender's text unchanged between Aboard's tags, and a
// reply instruction when a reply is expected.
func Format(m Message) string {
	attrs := [][2]string{{"board", m.Board}, {"from", "@" + m.FromName}}
	if !m.FromHuman {
		if m.Owner != "" {
			attrs = append(attrs, [2]string{"owner", m.Owner})
		}
		attrs = append(attrs, [2]string{"role", m.Role})
		if m.Harness != "" {
			attrs = append(attrs, [2]string{"harness", m.Harness})
		}
	}
	attrs = append(attrs, [2]string{"sender", m.Sender}, [2]string{"seq", strconv.Itoa(m.Seq)})
	if m.Urgent {
		attrs = append(attrs, [2]string{"urgent", "true"})
	}
	if m.ExpectsReply {
		attrs = append(attrs, [2]string{"expects-reply", "true"})
	}
	if m.ReplyToSeq > 0 {
		attrs = append(attrs, [2]string{"reply-to", strconv.Itoa(m.ReplyToSeq)})
	}
	var b strings.Builder
	b.WriteString("<aboard-message")
	for _, kv := range attrs {
		fmt.Fprintf(&b, ` %s="%s"`, kv[0], attrEscaper.Replace(kv[1]))
	}
	b.WriteString(">\n")
	body := EscapeBody(m.Body)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("</aboard-message>")
	if m.ExpectsReply {
		fmt.Fprintf(&b, "\nReply requested. Reply with: aboard say --reply %d \"…\"", m.Seq)
	}
	return b.String()
}

// Bundle writes several messages of one board delivered together, in the order given.
func Bundle(board string, ms []Message) string {
	var b strings.Builder
	b.WriteString(bundleOpen(board, len(ms)))
	for _, m := range ms {
		b.WriteString(Format(m) + "\n")
	}
	b.WriteString(bundleClose)
	return b.String()
}

// Group is one board's messages in a bundle.
type Group struct {
	Board    string
	Messages []Message
}

// Bundles writes the messages of several boards delivered together: one
// <aboard-messages> element per board, separated by a blank line.
func Bundles(groups []Group) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		if len(g.Messages) > 0 {
			parts = append(parts, Bundle(g.Board, g.Messages))
		}
	}
	return strings.Join(parts, "\n\n")
}

const bundleClose = "</aboard-messages>"

func bundleOpen(board string, count int) string {
	return `<aboard-messages board="` + attrEscaper.Replace(board) + `" count="` + strconv.Itoa(count) + "\">\n"
}

// BundleSize is the length in bytes of Bundles(groups), computed without building it.
func BundleSize(groups []Group) int {
	n, nonEmpty := 0, 0
	for _, g := range groups {
		if len(g.Messages) == 0 {
			continue
		}
		nonEmpty++
		n += len(bundleOpen(g.Board, len(g.Messages))) + len(bundleClose)
		for _, m := range g.Messages {
			n += len(Format(m)) + 1
		}
	}
	if nonEmpty > 1 {
		n += 2 * (nonEmpty - 1)
	}
	return n
}
