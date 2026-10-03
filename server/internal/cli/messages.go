package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// cliMessage is a message in the CLI's --json output: the API's message without its
// deprecated trust field, which only delivery daemons from older builds read, and they
// read it from the API.
type cliMessage struct {
	api.Message
	// Trust hides the embedded field of the same name (encoding/json prefers the
	// shallower one), and omitempty leaves it out.
	Trust *struct{} `json:"trust,omitempty"`
}

// cliMessages returns ms as the CLI's --json output shows them.
func cliMessages(ms []api.Message) []cliMessage {
	out := make([]cliMessage, 0, len(ms))
	for _, m := range ms {
		out = append(out, cliMessage{Message: m})
	}
	return out
}

// runSay posts a message as an agent.
func runSay(ctx context.Context, a *app, args []string) error {
	const use = `aboard say <text> [--to T[,T…]] [--reply MSG] [--urgent] [--expect-reply] [--as AGENT] [--board NAME] [--json]`
	fs := a.flags("say")
	var to listFlag
	fs.Var(&to, "to", "who to address: all, @name or role:R; comma-separated or repeated")
	reply := fs.String("reply", "", "the message this replies to: msg_…, 6, #6 or board-name#6")
	urgent := fs.Bool("urgent", false, "mark the message urgent: first in each recipient's next delivery")
	expectReply := fs.Bool("expect-reply", false, "ask the recipients to reply")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board to post on")
	pos, err := a.parse(fs, args, use, 1, -1)
	if err != nil {
		return err
	}
	body := strings.Join(pos, " ")
	if strings.TrimSpace(body) == "" {
		return usageError("The message text is empty.", use)
	}
	var ref messageRef
	if *reply != "" {
		if ref, err = parseMessageRef(*reply); err != nil {
			return err
		}
	}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req := api.PostMessageRequest{Body: body}
	if len(to) > 0 {
		targets := []string(to)
		req.To = &targets
	}
	if *urgent {
		req.Urgent = urgent
	}
	if *expectReply {
		req.ExpectsReply = expectReply
	}
	if *reply != "" {
		id, err := a.resolveMessageRef(ctx, ref, t, cred)
		if err != nil {
			return err
		}
		req.ReplyTo = &id
	}
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	r, err := c.api.PostMessageWithResponse(ctx, t.board, &api.PostMessageParams{}, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	m := r.JSON201
	a.emit(struct {
		Message cliMessage `json:"message"`
	}{cliMessage{Message: *m}}, fmt.Sprintf("Sent #%d to %s on %s\n", m.Seq, targetsText(m.To), m.Board))
	return nil
}

// runInbox prints an agent's unread messages and acknowledges them.
func runInbox(ctx context.Context, a *app, args []string) error {
	const use = "aboard inbox [--wait SECONDS] [--peek] [--limit N] [--as AGENT] [--board NAME] [--json]"
	fs := a.flags("inbox")
	wait := fs.Int("wait", 0, "seconds to wait for a message when there are none")
	peek := fs.Bool("peek", false, "show messages without acknowledging them")
	limit := fs.Int("limit", 0, "the most messages to return")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if *wait < 0 || *limit < 0 {
		return usageError("--wait and --limit can't be negative.", use)
	}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	timeout := time.Duration(*wait)*time.Second + requestTimeout
	c, err := a.client(ctx, t.server, cred.Token, timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	params := &api.GetInboxParams{}
	if *wait > 0 {
		params.Wait = wait
	}
	if *limit > 0 {
		params.Limit = limit
	}
	r, err := c.api.GetInboxWithResponse(ctx, params)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	in := r.JSON200
	msgs := in.Messages
	if msgs == nil {
		msgs = []api.Message{}
	}

	// Acknowledge before printing, so no message is shown as read without the server
	// knowing it was delivered.
	var acked *int
	if !*peek && len(msgs) > 0 {
		ack, err := c.api.AckInboxWithResponse(ctx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: msgs[len(msgs)-1].Seq})
		if err != nil {
			return c.unreachable(err)
		}
		if ack.JSON200 == nil {
			return apiError(ack.StatusCode(), ack.Body)
		}
		acked = &ack.JSON200.Cursor
	}

	wrapped := make([]string, 0, len(msgs))
	for _, m := range msgs {
		wrapped = append(wrapped, deliveryText(m))
	}
	var bundle *string
	text := in.Board + " · no new messages\n"
	if len(msgs) > 0 {
		b := bundleText(in.Board, msgs)
		bundle = &b
		text = fmt.Sprintf("%s · %d new\n", in.Board, len(msgs)) + strings.Join(wrapped, "\n\n") + "\n"
	}
	a.emit(struct {
		Board     string       `json:"board"`
		Agent     string       `json:"agent"`
		Messages  []cliMessage `json:"messages"`
		AckedUpTo *int         `json:"acked_up_to"`
		More      bool         `json:"more"`
		Wrapped   []string     `json:"wrapped"`
		Bundle    *string      `json:"bundle"`
	}{in.Board, in.Agent, cliMessages(msgs), acked, in.More, wrapped, bundle}, text)
	return nil
}

// readUsage is the usage line of aboard read.
const readUsage = "aboard read [--after SEQ | --before SEQ | --around SEQ] [--from @NAME] [--role R] [--to-me] [--limit N] [--markdown] [--as AGENT] [--board NAME] [--json]"

// defaultReadLimit is how many messages aboard read shows without --limit.
const defaultReadLimit = 50

// readFilter is what aboard read and aboard watch pass to the timeline API besides the
// window: which messages match, and how many to show.
type readFilter struct {
	from, role string
	toMe       bool
	limit      int
}

// params returns the API parameters for the filter; the caller adds the window.
func (f readFilter) params() api.ListMessagesParams {
	var p api.ListMessagesParams
	if f.from != "" {
		p.From = &f.from
	}
	if f.role != "" {
		p.Role = &f.role
	}
	if f.toMe {
		p.ToMe = &f.toMe
	}
	if f.limit > 0 {
		p.Limit = &f.limit
	}
	return p
}

// runRead prints the board's timeline as the agent sees it, without acknowledging.
func runRead(ctx context.Context, a *app, args []string) error {
	fs := a.flags("read")
	after := fs.Int("after", 0, "show the oldest messages after this sequence number")
	before := fs.Int("before", 0, "show the newest messages before this sequence number")
	around := fs.Int("around", 0, "show messages around this sequence number")
	from := fs.String("from", "", "only messages from this member (@name)")
	role := fs.String("role", "", "only messages from members with this role")
	toMe := fs.Bool("to-me", false, "only messages addressed to you that you didn't send")
	limit := fs.Int("limit", 0, "the most messages to show (default 50)")
	markdown := fs.Bool("markdown", false, "print a Markdown transcript to paste into a session")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, readUsage, 0, 0); err != nil {
		return err
	}
	if *after < 0 || *before < 0 || *around < 0 || *limit < 0 {
		return usageError("--after, --before, --around and --limit can't be negative.", readUsage)
	}
	if windows := btoi(*after > 0) + btoi(*before > 0) + btoi(*around > 0); windows > 1 {
		return usageError("Use only one of --after, --before and --around.", readUsage)
	}
	if *markdown && a.json {
		return usageError("--markdown and --json can't be combined.", readUsage)
	}
	f := readFilter{from: strings.TrimPrefix(*from, "@"), role: *role, toMe: *toMe, limit: *limit}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	page, err := readPage(ctx, c, t.board, f, *after, *before, *around)
	if err != nil {
		return err
	}
	msgs := page.Messages
	if msgs == nil {
		msgs = []api.Message{}
	}
	if *markdown {
		_, err := io.WriteString(a.env.Stdout, transcriptText(page.Board, msgs))
		return err
	}

	filtered := f.from != "" || f.role != "" || f.toMe || *after > 0 || *before > 0 || *around > 0
	text := page.Board + " · no messages yet\n"
	if filtered {
		text = page.Board + " · no messages\n"
	}
	if len(msgs) > 0 {
		text = fmt.Sprintf("%s · %d %s\n", page.Board, len(msgs), plural(len(msgs), "message", "messages")) + timelineText(msgs)
	}
	rest := readHintArgs(f, *as, *boardFlag)
	if page.PrevBefore != nil {
		text += fmt.Sprintf("Earlier: aboard read --before %d%s\n", *page.PrevBefore, rest)
	}
	if page.NextAfter != nil {
		text += fmt.Sprintf("Later: aboard read --after %d%s\n", *page.NextAfter, rest)
	}
	a.emit(struct {
		Board      string       `json:"board"`
		Visibility string       `json:"visibility"`
		Messages   []cliMessage `json:"messages"`
		NextAfter  *int         `json:"next_after"`
		PrevBefore *int         `json:"prev_before"`
	}{page.Board, string(b.Policy.Visibility), cliMessages(msgs), page.NextAfter, page.PrevBefore}, text)
	return nil
}

// readPage reads the page aboard read shows: the newest matching messages by default,
// the oldest after --after, the newest before --before, or for --around half the limit
// before the message and the rest from it on.
func readPage(ctx context.Context, c *client, board string, f readFilter, after, before, around int) (*api.MessagePage, error) {
	newest := true
	switch {
	case after > 0:
		p := f.params()
		p.After = &after
		return c.messages(ctx, board, p)
	case before > 0:
		p := f.params()
		p.Before, p.Newest = &before, &newest
		return c.messages(ctx, board, p)
	case around == 0:
		p := f.params()
		p.Newest = &newest
		return c.messages(ctx, board, p)
	}
	limit := f.limit
	if limit == 0 {
		limit = defaultReadLimit
	}
	earlier := &api.MessagePage{Board: board}
	if half := limit / 2; half > 0 {
		p := f.params()
		p.Before, p.Newest, p.Limit = &around, &newest, &half
		var err error
		if earlier, err = c.messages(ctx, board, p); err != nil {
			return nil, err
		}
	}
	p := f.params()
	from, rest := around-1, limit-limit/2
	p.After, p.Limit = &from, &rest
	later, err := c.messages(ctx, board, p)
	if err != nil {
		return nil, err
	}
	out := &api.MessagePage{Board: later.Board, Messages: append(earlier.Messages, later.Messages...)}
	out.PrevBefore, out.NextAfter = later.PrevBefore, later.NextAfter
	if len(earlier.Messages) > 0 {
		out.PrevBefore = earlier.PrevBefore
	}
	if len(later.Messages) == 0 {
		out.NextAfter = earlier.NextAfter
	}
	return out, nil
}

// readHintArgs returns the flags that repeat a read's filters in an Earlier or Later
// hint, with a leading space. --as and --board are kept when they were given, so the
// hint runs as shown.
func readHintArgs(f readFilter, as, board string) string {
	var b strings.Builder
	if f.from != "" {
		b.WriteString(" --from @" + f.from)
	}
	if f.role != "" {
		b.WriteString(" --role " + f.role)
	}
	if f.toMe {
		b.WriteString(" --to-me")
	}
	if f.limit > 0 {
		fmt.Fprintf(&b, " --limit %d", f.limit)
	}
	if as != "" {
		b.WriteString(" --as " + as)
	}
	if board != "" {
		b.WriteString(" --board " + board)
	}
	return b.String()
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
