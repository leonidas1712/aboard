package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
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
	use := usageOf("say")
	fs := a.flags("say")
	var to listFlag
	fs.Var(&to, "to", "who to address: all, @name or role:R; comma-separated or repeated")
	reply := fs.String("reply", "", "the message this replies to: msg_…, 6, #6 or board-name#6")
	urgent := fs.Bool("urgent", false, "mark the message urgent: first in each recipient's next delivery")
	expectReply := fs.Bool("expect-reply", false, "ask the recipients to reply")
	waitFor := fs.Int("wait-reply", 0, "ask for a reply and wait up to this many seconds for it")
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
	if *waitFor < 0 || *waitFor > 3600 {
		return usageError("--wait-reply takes from 1 to 3600 seconds.", use)
	}
	if *waitFor > 0 {
		*expectReply = true
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
	agent := delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: cred.Name}
	unread, recipients := a.unreadAfterSay(ctx, c, agent), recipientsOf(ctx, c, m)
	out := sayOutput{Message: cliMessage{Message: *m}, Unread: unread, Recipients: recipients}
	text := fmt.Sprintf("Sent #%d to %s on %s\n", m.Seq, targetsText(m.To), m.Board) + unreadText(m.Board, unread) + recipientsText(recipients)
	if *waitFor > 0 {
		wc, err := a.client(ctx, t.server, cred.Token, time.Duration(*waitFor)*time.Second+requestTimeout)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(*waitFor)*time.Second+requestTimeout)
		defer cancel()
		w, err := a.waitForReply(wctx, wc, agent, m, time.Duration(*waitFor)*time.Second)
		if err != nil {
			return err
		}
		out.Outcome, out.OwnerMessages, out.TimedOut = &w.outcome, ptrTo(cliMessages(w.owner)), ptrTo(w.outcome == waitTimeout)
		out.Reply = new(*cliMessage)
		if w.reply != nil {
			reply := cliMessage{Message: *w.reply}
			*out.Reply = &reply
		}
		text += waitedText(m, *waitFor, w)
	}
	a.emit(out, text)
	return nil
}

// sayOutput is aboard say's --json output (SayOutput in spec/cli.yaml).
type sayOutput struct {
	Message    cliMessage      `json:"message"`
	Unread     *unreadNote     `json:"unread"`
	Recipients []recipientNote `json:"recipients"`
	// The rest are set only with --wait-reply.
	Outcome       *string       `json:"outcome,omitempty"`
	Reply         **cliMessage  `json:"reply,omitempty"`
	OwnerMessages *[]cliMessage `json:"owner_messages,omitempty"`
	TimedOut      *bool         `json:"timed_out,omitempty"`
}

func ptrTo[T any](v T) *T { return &v }

// runInbox prints an agent's unread messages and acknowledges them.
func runInbox(ctx context.Context, a *app, args []string) error {
	use := usageOf("inbox")
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

	ref := delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: cred.Name}
	deadline := time.Now().Add(time.Duration(*wait) * time.Second)
	var (
		in    *api.Inbox
		msgs  []api.Message
		acked *int
	)
	for {
		if left := time.Until(deadline); *wait > 0 && left > 0 {
			// Wait on the server for something unread first, so the daemon holds the
			// agent's deliveries only while the command reads and acknowledges.
			secs := int((left + time.Second - 1) / time.Second)
			r, err := c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &secs, Limit: ptrTo(1)})
			if err != nil {
				return c.unreachable(err)
			}
			if r.JSON200 == nil {
				return apiError(r.StatusCode(), r.Body)
			}
		}
		if in, msgs, acked, err = a.readInbox(ctx, c, ref, *limit, !*peek, !*peek); err != nil {
			return err
		}
		// A message a session here already received isn't shown again. When that was
		// all there was, a wait goes on for something new.
		if len(msgs) > 0 || *wait == 0 || *peek || !time.Now().Before(deadline) {
			break
		}
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

// readInbox reads the agent's unread messages, leaving out those a session on this
// machine already received, and with ack acknowledges what it read; the server then
// tells the delivery daemon, so it never hands or names those messages again. It
// acknowledges before returning, so no message is shown as read without the server
// knowing. With confirm, a command run in the agent's own session confirms what was
// handed to it.
func (a *app) readInbox(ctx context.Context, c *client, ref delivery.AgentRef, limit int, ack, confirm bool) (*api.Inbox, []api.Message, *int, error) {
	rd := a.startInboxRead(ctx, ref, confirm)
	defer rd.done()
	params := &api.GetInboxParams{}
	if limit > 0 {
		params.Limit = &limit
	}
	r, err := c.api.GetInboxWithResponse(ctx, params)
	if err != nil {
		return nil, nil, nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, nil, nil, apiError(r.StatusCode(), r.Body)
	}
	in := r.JSON200
	msgs := rd.unread(in.Messages)
	if !ack || len(in.Messages) == 0 {
		return in, msgs, nil, nil
	}
	// Up to the last message read, including any a session already received: they are
	// read either way.
	res, err := c.api.AckInboxWithResponse(ctx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: in.Messages[len(in.Messages)-1].Seq})
	if err != nil {
		return nil, nil, nil, c.unreachable(err)
	}
	if res.JSON200 == nil {
		return nil, nil, nil, apiError(res.StatusCode(), res.Body)
	}
	return in, msgs, &res.JSON200.Cursor, nil
}

// readUsage is the usage line of aboard read.
var readUsage = usageOf("read")

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
	thread := fs.String("thread", "", "show the whole thread this message is in: msg_…, 6 or #6")
	threads := fs.Bool("threads", false, "list the board's threads, the newest activity first")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, readUsage, 0, 0); err != nil {
		return err
	}
	if *after < 0 || *before < 0 || *around < 0 || *limit < 0 {
		return usageError("--after, --before, --around and --limit can't be negative.", readUsage)
	}
	windows := btoi(*after > 0) + btoi(*before > 0) + btoi(*around > 0)
	if windows > 1 {
		return usageError("Use only one of --after, --before and --around.", readUsage)
	}
	if *markdown && a.json {
		return usageError("--markdown and --json can't be combined.", readUsage)
	}
	if *thread != "" && (windows > 0 || *from != "" || *role != "" || *toMe || *limit > 0) {
		return usageError("--thread shows a whole thread, so it can't be combined with --after, --before, --around, --from, --role, --to-me or --limit.", readUsage)
	}
	if *threads && (*thread != "" || windows > 0 || *from != "" || *role != "" || *toMe || *markdown) {
		return usageError("--threads lists every thread, so it can't be combined with --thread, --after, --before, --around, --from, --role, --to-me or --markdown.", readUsage)
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
	if *thread != "" {
		return readThread(ctx, a, c, t, cred, b, *thread, *markdown)
	}
	if *threads {
		return readThreads(ctx, a, c, b, *limit, readHintArgs(readFilter{}, *as, *boardFlag))
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

// readThread prints the thread the message ref points at: its first message, when the
// agent may see it, then every reply, oldest first.
func readThread(ctx context.Context, a *app, c *client, t target, cred agentCredential, b *api.Board, ref string, markdown bool) error {
	r, err := parseMessageRef(ref)
	if err != nil {
		return err
	}
	if r.Board != "" && r.Board != t.board {
		return newError("message_ref_invalid", fmt.Sprintf("%s reads threads on %s, its own board, not on %s.", cred.Name, t.board, r.Board),
			"Use the message's number on "+t.board+", or --as an agent on "+r.Board+".")
	}
	id, err := a.resolveMessageRef(ctx, r, t, cred)
	if err != nil {
		return err
	}
	th, err := c.thread(ctx, id)
	if e := (*Error)(nil); errors.As(err, &e) && e.Code == "message_not_found" {
		return newError("message_ref_invalid", fmt.Sprintf("There is no message %s on board %s that %s can see.", ref, t.board, cred.Name),
			"Run aboard read to see the messages and their numbers.")
	}
	if err != nil {
		return err
	}
	msgs := th.Replies
	var root *cliMessage
	rootSeq := 0
	if th.Root != nil {
		msgs = append([]api.Message{*th.Root}, th.Replies...)
		root, rootSeq = &cliMessage{Message: *th.Root}, th.Root.Seq
	} else if len(th.Replies) > 0 && th.Replies[0].ThreadRootSeq != nil {
		rootSeq = *th.Replies[0].ThreadRootSeq
	}
	if markdown {
		_, err := io.WriteString(a.env.Stdout, transcriptText(t.board, msgs))
		return err
	}
	text := fmt.Sprintf("%s · thread #%d · %s\n", t.board, rootSeq, repliesText(len(th.Replies), "no replies")) + timelineText(msgs)
	a.emit(struct {
		Board         string       `json:"board"`
		Visibility    string       `json:"visibility"`
		ThreadRootSeq int          `json:"thread_root_seq"`
		Root          *cliMessage  `json:"root"`
		Replies       []cliMessage `json:"replies"`
	}{t.board, string(b.Policy.Visibility), rootSeq, root, cliMessages(th.Replies)}, text)
	return nil
}

// repliesText says how many replies there are: "1 reply", "3 replies", or none.
func repliesText(n int, none string) string {
	if n == 0 {
		return none
	}
	return fmt.Sprintf("%d %s", n, plural(n, "reply", "replies"))
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
