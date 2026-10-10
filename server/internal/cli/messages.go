package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
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
	var attach listFlag
	var boardFiles listFlag
	fs.Var(&boardFiles, "file", "an existing board file by name or id, optionally @vN; repeat for several")
	fs.Var(&attach, "attach", "a local file to put on the board and attach")
	fs.Var(&to, "to", "who to address: all, @name, role:R, owner:handle or mine (person only); comma-separated or repeated")
	task := fs.String("task", "", "the task this message is about")
	noTask := fs.Bool("no-task", false, "do not inherit a task from your current task or the thread")
	reply := fs.String("reply", "", "the message this replies to: msg_…, 6, #6 or board-name#6")
	urgent := fs.Bool("urgent", false, "mark the message urgent: first in each recipient's next delivery")
	expectReply := fs.Bool("expect-reply", false, "ask the recipients to reply")
	waitFor := fs.Int("wait-reply", 0, "ask for a reply and wait up to this many seconds for it")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board to post on")
	option := fs.Int("option", 0, "answer the ask with this numbered option")
	pos, err := a.parse(fs, args, use, 0, -1)
	if err != nil {
		return err
	}
	optionGiven := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "option" {
			optionGiven = true
		}
	})
	if *waitFor < 0 || *waitFor > 3600 {
		return usageError("--wait-reply takes from 1 to 3600 seconds.", use)
	}
	if *task != "" && *noTask {
		return usageError("Use only one of --task and --no-task.", use)
	}
	if optionGiven && (*reply == "" || *option < 1 || *option > 4) {
		return usageError("--option takes 1 to 4 and needs --reply.", use)
	}
	if *option != 0 && !a.agentSelected(*as) {
		return runAskOption(ctx, a, *boardFlag, *as, *task, *noTask, *reply, *option, strings.Join(pos, " "), to, *urgent, *expectReply, *waitFor, boardFiles, attach)
	}
	body := strings.Join(pos, " ")
	if strings.TrimSpace(body) == "" && *option == 0 {
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
	mine := slices.Contains([]string(to), "mine")
	var t target
	var cred agentCredential
	if mine {
		if a.agentSelected(*as) {
			return newError("human_command_in_session", "Only a person may use --to mine.", "An agent uses --to owner:<handle> with its own seat.")
		}
		if t, err = a.humanBoard(ctx, *boardFlag); err != nil {
			return err
		}
		if cred.Token, err = a.readOwnerToken(t.server); err != nil {
			return err
		}
		personClient, err := a.client(ctx, t.server, cred.Token, requestTimeout)
		if err != nil {
			return err
		}
		person, err := personClient.api.GetMeWithResponse(ctx)
		if err != nil {
			return personClient.unreachable(err)
		}
		if person.JSON200 == nil {
			return apiError(person.StatusCode(), person.Body)
		}
		cred.Name = person.JSON200.Name
		for i, target := range to {
			if target == "mine" {
				to[i] = "owner:" + cred.Name
			}
		}
	} else {
		t, cred, err = a.agentTaskTarget(ctx, *boardFlag, *as, *task)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req := api.PostMessageRequest{Body: body}
	if *task != "" {
		req.About = ptrTo([]api.TaskSelector{*task})
	} else if *noTask {
		req.About = ptrTo([]api.TaskSelector{})
	}
	if *task != "" || *noTask {
		if err := cTaskFeature(ctx, a, t, cred); err != nil {
			return err
		}
	}
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
	files, err := a.sayFiles(ctx, c, t, boardFiles, attach)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		req.Files = &files
	}

	if *option != 0 {
		if err := c.requireAsks(ctx); err != nil {
			return err
		}
		req.Answer = &api.AnswerRequest{Option: option}
		if strings.TrimSpace(req.Body) == "" {
			req.Body, err = askOptionBody(ctx, c, t.board, *req.ReplyTo, *option)
			if err != nil {
				return err
			}
		}
	}
	r, err := c.api.PostMessageWithResponse(ctx, t.board, &api.PostMessageParams{}, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	m := r.JSON201
	agent := delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: cred.Name, MemberID: cred.MemberID}
	var unread *unreadNote
	if !mine {
		unread = a.unreadAfterSay(ctx, c, agent)
	}
	recipients := recipientsOf(ctx, c, m)
	out := sayOutput{Message: cliMessage{Message: *m}, Unread: unread, Recipients: recipients, Warning: wakeWarning(m, recipients)}
	text := fmt.Sprintf("Sent #%d to %s on %s", m.Seq, targetsText(m.To), m.Board)
	if m.About != nil && len(*m.About) > 0 {
		refs := []string{}
		for _, tag := range *m.About {
			refs = append(refs, tag.Ref)
		}
		text += " · about " + strings.Join(refs, ", ")
	}
	text += "\n" + unreadText(m.Board, unread) + recipientsText(recipients)
	if w := out.Warning; w != nil {
		text += a.out().warn("Warning ("+w.Code+"): "+w.Message+" "+w.Hint) + "\n"
	}
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
	if !mine {
		out.Nudges = a.taskNudges(ctx, c, agent, c.taskInbox(ctx), "say", *boardFlag != "", m)
	}
	text += nudgesText(out.Nudges)
	a.emit(out, text)
	return nil
}

// sayOutput is aboard say's --json output (SayOutput in spec/cli.yaml).
type sayOutput struct {
	Message    cliMessage           `json:"message"`
	Unread     *unreadNote          `json:"unread"`
	Recipients []recipientNote      `json:"recipients"`
	Warning    *sayWarning          `json:"warning"`
	Nudges     []deliverytext.Nudge `json:"nudges"`
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
	queued := fs.Bool("queued", false, "preview this session's queued messages without acknowledging them")
	limit := fs.Int("limit", 0, "the most messages to return")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if *wait < 0 || *limit < 0 {
		return usageError("--wait and --limit can't be negative.", use)
	}
	if *queued {
		if *wait != 0 {
			return usageError("--queued does not wait or acknowledge messages.", use)
		}
		return a.runQueuedInbox(ctx, *boardFlag, *as, *limit)
	}
	// In a session with several seats and no --board, the inbox covers every seat.
	if key, ok := a.sessionKey(); ok && *boardFlag == "" && *as == "" && strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")) == "" {
		if seats, err := a.sessionAgents(ctx, key); err == nil {
			seats, err = a.filterAgentSeats(seats, *boardFlag)
			if err != nil {
				return err
			}
			if len(seats) > 1 {
				creds, err := a.readCredentials()
				if err != nil {
					return err
				}
				out, err := a.inboxSeats(ctx, seats, creds, *limit, !*peek, *wait)
				if err != nil {
					return err
				}
				a.emit(out, inboxSeatsText(out))
				return nil
			}
		}
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

	ref := delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: cred.Name, MemberID: cred.MemberID}
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
			r, err := c.waitInbox(ctx, deadline, api.GetInboxParams{Wait: &secs, Limit: ptrTo(1)})
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
	nudges := a.taskNudges(ctx, c, ref, in, "inbox", *boardFlag != "")
	text += nudgesText(nudges)
	// In a session, the boards its person was added to that are still new to them.
	var added []addedNotice
	if key, ok := a.sessionKey(); ok {
		_, added = a.sessionAdded(ctx, key)
		text += addedText(added)
	}
	a.emit(struct {
		Board     string               `json:"board"`
		Agent     string               `json:"agent"`
		Messages  []cliMessage         `json:"messages"`
		AckedUpTo *int                 `json:"acked_up_to"`
		More      bool                 `json:"more"`
		Wrapped   []string             `json:"wrapped"`
		Bundle    *string              `json:"bundle"`
		Added     []addedNotice        `json:"added,omitempty"`
		Work      *api.AgentWork       `json:"work,omitempty"`
		Nudges    []deliverytext.Nudge `json:"nudges"`
	}{in.Board, in.Agent, cliMessages(msgs), acked, in.More, wrapped, bundle, added, in.Work, nudges}, text)
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
	from, role, task string
	toMe             bool
	limit            int
}

// params returns the API parameters for the filter; the caller adds the window.
func (f readFilter) params() api.ListMessagesParams {
	var p api.ListMessagesParams
	if f.task != "" {
		p.Task = &f.task
	}
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
	task := fs.String("task", "", "only messages about this task")
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
	markRead := fs.Bool("mark-read", false, "show your unread messages, as yourself, and mark the ones shown read")
	receipts := fs.String("receipts", "", "whether this message has reached each recipient: msg_…, 6 or #6")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, readUsage, 0, 0); err != nil {
		return err
	}
	if *task != "" && (*markRead || *receipts != "" || *thread != "" || *threads) {
		return usageError("--task filters timeline messages; use it without --mark-read, --receipts, --thread or --threads.", readUsage)
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
	if *receipts != "" {
		if *markRead || *thread != "" || *threads || windows > 0 || *from != "" || *role != "" || *toMe || *limit > 0 || *markdown {
			return usageError("--receipts shows one message's receipts, so it can be combined only with --as and --board.", readUsage)
		}
		return runReadReceipts(ctx, a, *receipts, *boardFlag, *as)
	}
	if *markRead {
		if *thread != "" || *threads || windows > 0 || *from != "" || *role != "" || *toMe || *markdown {
			return usageError("--mark-read shows your unread messages from your read position on, so it can't be combined with --thread, --threads, --after, --before, --around, --from, --role, --to-me or --markdown.", readUsage)
		}
		if *as != "" {
			return usageError("--mark-read moves your own read position, as a person. An agent's moves with aboard inbox.", readUsage)
		}
		return runReadMarked(ctx, a, *boardFlag, *limit)
	}
	f := readFilter{from: strings.TrimPrefix(*from, "@"), role: *role, toMe: *toMe, limit: *limit, task: *task}
	t, cred, err := a.agentTaskTarget(ctx, *boardFlag, *as, *task)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	if *task != "" {
		if err := c.requireTasks(ctx); err != nil {
			return err
		}
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
	shown := a.beginShownRead(ctx, cred, b)
	defer shown.done()
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
		if err == nil {
			shown.report(ctx, msgs)
		}
		return err
	}

	filtered := f.task != "" || f.from != "" || f.role != "" || f.toMe || *after > 0 || *before > 0 || *around > 0
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
	err = a.emitChecked(struct {
		Board      string       `json:"board"`
		Visibility string       `json:"visibility"`
		Messages   []cliMessage `json:"messages"`
		NextAfter  *int         `json:"next_after"`
		PrevBefore *int         `json:"prev_before"`
	}{page.Board, string(b.Policy.Visibility), cliMessages(msgs), page.NextAfter, page.PrevBefore}, text)
	if err == nil {
		shown.report(ctx, msgs)
	}
	return err
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
	shown := a.beginShownRead(ctx, cred, b)
	defer shown.done()
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
		if err == nil {
			shown.report(ctx, msgs)
		}
		return err
	}
	text := fmt.Sprintf("%s · thread #%d · %s\n", t.board, rootSeq, repliesText(len(th.Replies), "no replies")) + timelineText(msgs)
	err = a.emitChecked(struct {
		Board         string       `json:"board"`
		Visibility    string       `json:"visibility"`
		ThreadRootSeq int          `json:"thread_root_seq"`
		Root          *cliMessage  `json:"root"`
		Replies       []cliMessage `json:"replies"`
	}{t.board, string(b.Policy.Visibility), rootSeq, root, cliMessages(th.Replies)}, text)
	if err == nil {
		shown.report(ctx, msgs)
	}
	return err
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
	if f.task != "" {
		b.WriteString(" --task " + commandWord(f.task))
	}
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
