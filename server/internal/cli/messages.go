package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// agentTarget resolves the board and the agent for an agent command.
func (a *app) agentTarget(boardFlag, asFlag string) (target, agentCredential, error) {
	t, err := a.selectBoard(boardFlag)
	if err != nil {
		return target{}, agentCredential{}, err
	}
	cred, err := a.agentFor(asFlag, t)
	if err != nil {
		return target{}, agentCredential{}, err
	}
	return t, cred, nil
}

// runSay posts a message as an agent.
func runSay(ctx context.Context, a *app, args []string) error {
	const use = `aboard say <text> [--to T[,T…]] [--reply MSG] [--urgent] [--expect-reply] [--as AGENT] [--board NAME] [--json]`
	fs := a.flags("say")
	var to listFlag
	fs.Var(&to, "to", "who to address: all, @name or role:R; comma-separated or repeated")
	reply := fs.String("reply", "", "the message this replies to: msg_…, 6, #6 or board-name#6")
	urgent := fs.Bool("urgent", false, "mark the message urgent")
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
	t, cred, err := a.agentTarget(*boardFlag, *as)
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
	c, err := a.client(t.server, cred.Token, requestTimeout)
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
		Message *api.Message `json:"message"`
	}{m}, fmt.Sprintf("Sent #%d to %s on %s\n", m.Seq, targetsText(m.To), m.Board))
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
	t, cred, err := a.agentTarget(*boardFlag, *as)
	if err != nil {
		return err
	}
	timeout := time.Duration(*wait)*time.Second + requestTimeout
	c, err := a.client(t.server, cred.Token, timeout)
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
		Board     string        `json:"board"`
		Agent     string        `json:"agent"`
		Messages  []api.Message `json:"messages"`
		AckedUpTo *int          `json:"acked_up_to"`
		More      bool          `json:"more"`
		Wrapped   []string      `json:"wrapped"`
		Bundle    *string       `json:"bundle"`
	}{in.Board, in.Agent, msgs, acked, in.More, wrapped, bundle}, text)
	return nil
}

// runRead prints the board's timeline as the agent sees it, without acknowledging.
func runRead(ctx context.Context, a *app, args []string) error {
	const use = "aboard read [--after SEQ] [--limit N] [--as AGENT] [--board NAME] [--json]"
	fs := a.flags("read")
	after := fs.Int("after", 0, "show messages after this sequence number")
	limit := fs.Int("limit", 0, "the most messages to show")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if *after < 0 || *limit < 0 {
		return usageError("--after and --limit can't be negative.", use)
	}
	t, cred, err := a.agentTarget(*boardFlag, *as)
	if err != nil {
		return err
	}
	c, err := a.client(t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	page, err := c.messages(ctx, t.board, *after, *limit)
	if err != nil {
		return err
	}
	msgs := page.Messages
	if msgs == nil {
		msgs = []api.Message{}
	}
	text := page.Board + " · no messages yet\n"
	if len(msgs) > 0 {
		text = fmt.Sprintf("%s · %d %s\n", page.Board, len(msgs), plural(len(msgs), "message", "messages")) + timelineText(msgs)
	}
	a.emit(struct {
		Board      string        `json:"board"`
		Visibility string        `json:"visibility"`
		Messages   []api.Message `json:"messages"`
		NextAfter  *int          `json:"next_after"`
	}{page.Board, string(b.Policy.Visibility), msgs, page.NextAfter}, text)
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
