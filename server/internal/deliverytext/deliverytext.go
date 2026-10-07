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
	// About lists permanent task references recorded on this message.
	About []string
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
	// ReplyToFrom is the name of the member who sent the message this one replies to,
	// without the "@", or empty.
	ReplyToFrom string
	// To are the message's targets: all, @name or role:R. Nil when not known.
	To []string
	// Mentions are the names of the agents the message's mentions wake (`@name` or
	// `@role:R` in the body, resolved by the server when it was posted).
	Mentions []string
	// Reactions are the message's reactions, one per emoji, as a digest line counts
	// them.
	Reactions []Reaction
	Body      string
	// Truncated marks a body cut short to fit where it is shown.
	Truncated bool
}

// Context adds the receiving seat and explicit board commands when a session works
// on several boards. An empty context keeps the single-board delivery format.
type Context struct {
	Seat           string
	BoardQualified bool
}

func contextOf(contexts []Context) Context {
	if len(contexts) == 0 {
		return Context{}
	}
	return contexts[0]
}

func seatAttribute(contexts []Context) string {
	if seat := contextOf(contexts).Seat; seat != "" {
		return ` seat="` + attrEscaper.Replace(seat) + `"`
	}
	return ""
}

func boardCommand(command, board string, contexts []Context) string {
	if contextOf(contexts).BoardQualified {
		return command + " --board " + board
	}
	return command
}

// Reaction is one emoji on a message and how many members reacted with it.
type Reaction struct {
	Emoji string
	Count int
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
func Format(m Message, contexts ...Context) string {
	attrs := [][2]string{{"board", m.Board}}
	if seat := contextOf(contexts).Seat; seat != "" {
		attrs = append(attrs, [2]string{"seat", seat})
	}
	attrs = append(attrs, [2]string{"from", "@" + m.FromName})
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
	if len(m.About) > 0 {
		attrs = append(attrs, [2]string{"about", strings.Join(m.About, " ")})
	}
	if m.Urgent {
		attrs = append(attrs, [2]string{"urgent", "true"})
	}
	if m.ExpectsReply {
		attrs = append(attrs, [2]string{"expects-reply", "true"})
	}
	if m.ReplyToSeq > 0 {
		attrs = append(attrs, [2]string{"reply-to", strconv.Itoa(m.ReplyToSeq)})
	}
	if m.Truncated {
		attrs = append(attrs, [2]string{"truncated", "true"})
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
		fmt.Fprintf(&b, "\nReply requested. Reply with: %s --reply %d \"…\"", boardCommand("aboard say", m.Board, contexts), m.Seq)
	}
	return b.String()
}

// Bundle writes several messages of one board delivered together, in the order given.
func Bundle(board string, ms []Message, contexts ...Context) string {
	var b strings.Builder
	b.WriteString(bundleOpen(board, len(ms), contexts...))
	for _, m := range ms {
		b.WriteString(Format(m, contexts...) + "\n")
	}
	b.WriteString(bundleClose)
	if contextOf(contexts).BoardQualified {
		fmt.Fprintf(&b, "\nUse %s to reply, or %s to read this board.", boardCommand("aboard say", board, contexts), boardCommand("aboard read", board, contexts))
	}
	return b.String()
}

// Group is one board's messages in a bundle.
type Group struct {
	Context  Context
	Board    string
	Messages []Message
}

// Bundles writes the messages of several boards delivered together: one
// <aboard-messages> element per board, separated by a blank line.
func Bundles(groups []Group) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		if len(g.Messages) > 0 {
			parts = append(parts, Bundle(g.Board, g.Messages, g.Context))
		}
	}
	return strings.Join(parts, "\n\n")
}

const bundleClose = "</aboard-messages>"

func bundleOpen(board string, count int, contexts ...Context) string {
	return `<aboard-messages board="` + attrEscaper.Replace(board) + `"` + seatAttribute(contexts) + ` count="` + strconv.Itoa(count) + "\">\n"
}

// Quiet writes the messages that waited for an agent's next turn without waking it, in
// a block of their own after an Aboard line that says so.
func Quiet(board string, ms []Message, contexts ...Context) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Aboard: while you were away, %s arrived on %s. They didn't wake you; read them, and answer only if one needs you:\n",
		countOf(len(ms), "other message"), board)
	b.WriteString(`<aboard-messages board="` + attrEscaper.Replace(board) + `"` + seatAttribute(contexts) + ` count="` + strconv.Itoa(len(ms)) + "\" quiet=\"true\">\n")
	for _, m := range ms {
		b.WriteString(Format(m, contexts...) + "\n")
	}
	b.WriteString(bundleClose)
	return b.String()
}

// Woken writes the messages a bundle or a turn's start carries for an agent that sees
// some quietly: the ones that concern it, then, in a quiet block, the ones that waited
// for its next turn. Either may be empty.
func Woken(board string, concern, quiet []Message, contexts ...Context) string {
	var parts []string
	if len(concern) > 0 {
		parts = append(parts, Bundle(board, concern, contexts...))
	}
	if len(quiet) > 0 {
		parts = append(parts, Quiet(board, quiet, contexts...))
	}
	return strings.Join(parts, "\n\n")
}

// digestLineBody is how much of a message's first line a digest line shows, in
// characters.
const digestLineBody = 80

// DigestLine is one message in a digest, made the same way every time from the message
// alone: "#17 @codex → all · reply to #12: the body's first line, cut short…". Any "<"
// is written "&lt;", so a body can't end the digest's element.
func DigestLine(m Message) string {
	to := "all"
	if len(m.To) > 0 {
		to = strings.Join(m.To, ", ")
	}
	line := fmt.Sprintf("#%d @%s → %s", m.Seq, m.FromName, to)
	if m.ReplyToSeq > 0 {
		line += fmt.Sprintf(" · reply to #%d", m.ReplyToSeq)
	}
	if m.Urgent {
		line += " · urgent"
	}
	if m.ExpectsReply {
		line += " · asks for a reply"
	}
	for _, r := range m.Reactions {
		line += fmt.Sprintf(" · %s %d", r.Emoji, r.Count)
	}
	first := ""
	for l := range strings.SplitSeq(m.Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			first = l
			break
		}
	}
	if r := []rune(first); len(r) > digestLineBody {
		first = strings.TrimSpace(string(r[:digestLineBody])) + "…"
	}
	return strings.ReplaceAll(line+": "+first, "<", "&lt;")
}

// DigestLinesBytes is how long a digest's lines may be together before they are grouped
// by sender, one line each.
const DigestLinesBytes = 4 << 10

// digestLines writes one line per message, or, when those pass DigestLinesBytes, one
// line per sender, in the order of its first message.
func digestLines(ms []Message) string {
	lines := make([]string, 0, len(ms))
	n := 0
	for _, m := range ms {
		l := DigestLine(m)
		lines = append(lines, l)
		n += len(l) + 1
	}
	if n <= DigestLinesBytes {
		return strings.Join(lines, "\n")
	}
	var order []string
	seqs := map[string][]string{}
	for _, m := range ms {
		if _, ok := seqs[m.FromName]; !ok {
			order = append(order, m.FromName)
		}
		seqs[m.FromName] = append(seqs[m.FromName], "#"+strconv.Itoa(m.Seq))
	}
	lines = lines[:0]
	for _, name := range order {
		lines = append(lines, fmt.Sprintf("@%s: %s: %s", name, countOf(len(seqs[name]), "message"), strings.Join(seqs[name], ", ")))
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), "<", "&lt;")
}

// Digest writes a big backlog: the messages that concern the agent in full, and one line
// for every other message, ending with the commands that read them in full. summarized
// must not be empty.
func Digest(board string, full, summarized []Message, contexts ...Context) string {
	var b strings.Builder
	total := len(full) + len(summarized)
	if len(full) == 0 {
		fmt.Fprintf(&b, "Aboard: while you were away, %s arrived on %s. None of them concerns you; each is one line:\n",
			countOf(total, "message"), board)
	} else {
		fmt.Fprintf(&b, "Aboard: %s arrived on %s. The %d that concern you are in full; the other %d are one line each.\n",
			countOf(total, "message"), board, len(full), len(summarized))
		b.WriteString(Bundle(board, full, contexts...) + "\n")
	}
	b.WriteString(`<aboard-digest board="` + attrEscaper.Replace(board) + `"` + seatAttribute(contexts) + ` count="` + strconv.Itoa(len(summarized)) + "\">\n")
	b.WriteString(digestLines(summarized) + "\n</aboard-digest>\n")
	first := summarized[0].Seq
	for _, m := range summarized {
		first = min(first, m.Seq)
	}
	fmt.Fprintf(&b, "Read one in full with %s --around <seq>, everything from the first with %s --after %d, or the board's threads with %s --threads.",
		boardCommand("aboard read", board, contexts), boardCommand("aboard read", board, contexts), first-1, boardCommand("aboard read", board, contexts))
	return b.String()
}

// countOf writes "1 message" or "3 messages".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// BundleSize is the length in bytes of Bundles(groups), computed without building it.
func BundleSize(groups []Group) int {
	n, nonEmpty := 0, 0
	for _, g := range groups {
		if len(g.Messages) == 0 {
			continue
		}
		nonEmpty++
		n += len(bundleOpen(g.Board, len(g.Messages), g.Context)) + len(bundleClose)
		if g.Context.BoardQualified {
			n += len(fmt.Sprintf("\nUse %s to reply, or %s to read this board.", boardCommand("aboard say", g.Board, []Context{g.Context}), boardCommand("aboard read", g.Board, []Context{g.Context})))
		}
		for _, m := range g.Messages {
			n += len(Format(m, g.Context)) + 1
		}
	}
	if nonEmpty > 1 {
		n += 2 * (nonEmpty - 1)
	}
	return n
}

// Notice writes the content-free notice that tells a busy agent which messages are
// waiting for it: each one's sequence number, sender name, the sender's owner when shown,
// and sender label, all escaped. It never holds a body or anything else a sender wrote.
func Notice(board string, ms []Message, contexts ...Context) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		from := m.FromName
		if !m.FromHuman && m.Owner != "" {
			from = m.Owner + "'s " + from
		}
		parts = append(parts, fmt.Sprintf("#%d from %s (%s)", m.Seq, from, m.Sender))
	}
	text := fmt.Sprintf("%d waiting on %s: %s; run %s when convenient", len(ms), board, strings.Join(parts, ", "), boardCommand("aboard inbox", board, contexts))
	return `<aboard-notice board="` + attrEscaper.Replace(board) + `"` + seatAttribute(contexts) + ` waiting="` + strconv.Itoa(len(ms)) + `">` +
		attrEscaper.Replace(text) + "</aboard-notice>"
}

// ModeRule says, in a sentence or two, what an agent's delivery mode means for when it
// wakes, and how to address a message so the agents it is for act on it soon. mode is
// focused, all, humans or off; anything else reads as focused, the default.
func ModeRule(mode string) string {
	switch mode {
	case "all":
		return "Every message wakes you, and every other agent in all mode, so post to everyone sparingly " +
			"and address the agents a message is for (--to @name or --to role:R)."
	case "humans":
		return "Only messages from people wake you; messages from agents wait until a person's message wakes you, " +
			"or until you run aboard inbox."
	case "off":
		return "Nothing wakes you or arrives by itself: read your messages with aboard inbox, " +
			"or wait for one with aboard inbox --wait 60."
	default:
		return "A message to everyone wakes only the agents it mentions in focused mode, you included; the others get it quietly at their next turn. " +
			"To make an agent act soon, address or mention it (--to @name, --to role:R, or @name in the text) or ask with --expect-reply."
	}
}

// ModeLine names an agent's delivery mode and its rule, for the places an agent learns
// about its seat: "Delivery mode: focused. A message to everyone …".
func ModeLine(mode string) string {
	return "Delivery mode: " + mode + ". " + ModeRule(mode)
}

// ModeChanged tells an agent, at its next turn or delivery, that its delivery mode on
// board changed, and what the new one means.
func ModeChanged(board, from, to string) string {
	return fmt.Sprintf("Aboard: your delivery mode on %s changed from %s to %s. %s", board, from, to, ModeRule(to))
}

// Reopened tells a session that started again with the same session id which agent it
// is again, and that agent's delivery mode when mode isn't empty. turnEnd says the
// messages that waited arrive when this turn ends: for a harness whose session start
// comes before its first turn, with no hook waiting yet.
func Reopened(name, board, mode string, turnEnd bool) string {
	note := fmt.Sprintf("Aboard: this session is %s on %s again, as it was before it closed", name, board)
	if turnEnd {
		note += fmt.Sprintf("; messages that waited for %s arrive when this turn ends", name)
	}
	note += "."
	if mode != "" {
		note += " " + ModeLine(mode)
	}
	return note
}

// Lost tells a session that started again that another session resumed the agent it
// filled meanwhile, so it has none now, and how to take the agent back.
func Lost(name, board string) string {
	return fmt.Sprintf("Aboard: this session was %s on %s until another session resumed %s; it has no agent now. "+
		"To act as %s here again, run aboard resume %s, which leaves the other session without it.", name, board, name, name, name)
}
