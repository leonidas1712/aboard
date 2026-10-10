package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// readMarkOutput is aboard read --mark-read's --json output.
type readMarkOutput struct {
	Board    string       `json:"board"`
	Messages []cliMessage `json:"messages"`
	ReadUpTo int          `json:"read_up_to"`
	Unread   int          `json:"unread"`
}

// runReadMarked shows the person's unread messages on a board, read with their own
// login from their read position on, and marks read the ones it showed: never one it
// didn't show, so the count stays true in the board view and on other machines.
func runReadMarked(ctx context.Context, a *app, boardFlag string, limit int) error {
	if err := a.refuseInSession("Marking messages read as yourself", "aboard read --mark-read"+boardArg(a.namedBoard(boardFlag))); err != nil {
		return err
	}
	t, err := a.humanBoard(ctx, boardFlag)
	if err != nil {
		return err
	}
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	if b.ReadUpTo == nil || b.Unread == nil {
		return newError("not_on_board", fmt.Sprintf("You aren't on board %s, so you have nothing unread there.", b.Name),
			"It is open, so you can join it: aboard board add @me --board "+b.Name+".")
	}
	from := *b.ReadUpTo
	params := readFilter{limit: limit}.params()
	params.After = &from
	page, err := c.messages(ctx, t.board, params)
	if err != nil {
		return err
	}
	out := readMarkOutput{Board: b.Name, Messages: cliMessages(page.Messages), ReadUpTo: from, Unread: *b.Unread}
	if out.Messages == nil {
		out.Messages = []cliMessage{}
	}
	if n := len(page.Messages); n > 0 {
		r, err := c.api.AckBoardWithResponse(ctx, t.board, nil, api.AckBoardJSONRequestBody{UpTo: page.Messages[n-1].Seq})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		out.ReadUpTo, out.Unread = r.JSON200.ReadUpTo, r.JSON200.Unread
	}
	// The header counts what was unread; the page also shows the person's own messages
	// among them, which never count.
	text := b.Name + " · nothing unread\n"
	if *b.Unread > 0 {
		text = fmt.Sprintf("%s · %d unread\n", b.Name, *b.Unread)
	}
	if len(page.Messages) > 0 {
		text += timelineText(page.Messages) + fmt.Sprintf("Marked read up to #%d.\n", out.ReadUpTo)
	}
	if out.Unread > 0 {
		text += fmt.Sprintf("%d more unread: aboard read --mark-read%s\n", out.Unread, boardArg(boardFlag))
	}
	a.emit(out, text)
	return nil
}

// runReadReceipts says whether a message has reached each member it was addressed to.
// It reads as the selected agent when there is one, on its board; otherwise with the
// person's login.
func runReadReceipts(ctx context.Context, a *app, ref, boardFlag, asFlag string) error {
	r, err := parseMessageRef(ref)
	if err != nil {
		return err
	}
	var (
		t     target
		c     *client
		agent string
	)
	if a.agentSelected(asFlag) {
		var cred agentCredential
		if t, cred, err = a.agentTarget(ctx, boardFlag, asFlag); err != nil {
			return err
		}
		if r.Board != "" && r.Board != t.board {
			return newError("message_ref_invalid", fmt.Sprintf("%s reads receipts on %s, its own board, not on %s.", cred.Name, t.board, r.Board),
				"Use the message's number on "+t.board+", or --as an agent on "+r.Board+".")
		}
		agent = cred.Name
		c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
	} else {
		if t, err = a.humanBoard(ctx, boardFlag); err != nil {
			return err
		}
		if r.Board != "" {
			t.board = r.Board
		}
		c, err = a.humanClient(ctx, t)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	seq := r.Seq
	if r.ID != "" {
		if seq, err = seqOf(ctx, c, r.ID); err != nil {
			return err
		}
	}
	res, err := c.api.GetReceiptsWithResponse(ctx, t.board, seq)
	if err != nil {
		return c.unreachable(err)
	}
	if res.JSON200 == nil {
		e := apiError(res.StatusCode(), res.Body)
		if x := asError(e); x.Code == "message_not_found" {
			who := "you"
			if agent != "" {
				who = agent
			}
			return newError("message_ref_invalid", fmt.Sprintf("There is no message %s on board %s that %s can see.", ref, t.board, who),
				"Run aboard read to see the messages and their numbers.")
		}
		return e
	}
	a.emit(res.JSON200, receiptsText(res.JSON200))
	return nil
}

// seqOf finds the sequence number of the message with this id, through its thread.
func seqOf(ctx context.Context, c *client, id string) (int, error) {
	th, err := c.thread(ctx, id)
	if err != nil {
		return 0, err
	}
	if th.Root != nil && th.Root.Id == id {
		return th.Root.Seq, nil
	}
	for _, m := range th.Replies {
		if m.Id == id {
			return m.Seq, nil
		}
	}
	return 0, newError("message_ref_invalid", fmt.Sprintf("There is no message %s that you can see.", id),
		"Run aboard read to see the messages and their numbers.")
}

// receiptsText is aboard read --receipts' text output.
func receiptsText(r *api.Receipts) string {
	if !r.Available {
		return fmt.Sprintf("%s · #%d · receipts unavailable: recipients were not recorded for this message\n", r.Board, r.Seq)
	}
	if r.ToEveryone {
		return fmt.Sprintf("%s · #%d to everyone · receipts are kept only for messages to someone\n", r.Board, r.Seq)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · #%d to %s\n", r.Board, r.Seq, strings.Join(r.To, ", "))
	if len(r.Recipients) == 0 {
		b.WriteString("  no recipient is on the board now\n")
	}
	width := 0
	for _, rc := range r.Recipients {
		width = max(width, len(rc.Member.Name))
	}
	for _, rc := range r.Recipients {
		line := fmt.Sprintf("  %-*s  %s", width, rc.Member.Name, rc.State)
		if rc.State == api.ReceiptStatePending && rc.Presence != nil {
			line += " · " + presenceNow(*rc.Presence)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// presenceNow says an agent's presence as it is at the moment.
func presenceNow(p api.Presence) string {
	switch p {
	case api.PresenceNoSession:
		return "no session now"
	case api.PresenceWaiting:
		return "waiting for its person now"
	case api.PresenceWorking, api.PresenceIdle:
	}
	return string(p) + " now"
}
