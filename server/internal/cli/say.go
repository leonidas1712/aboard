package cli

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// unreadNote is the sender's own unread messages on the board, as aboard say reports
// them after posting.
type unreadNote struct {
	Count int          `json:"count"`
	Seqs  []int        `json:"seqs"`
	From  []unreadFrom `json:"from"`
}

type unreadFrom struct {
	Name   string `json:"name"`
	Sender string `json:"sender"`
}

// recipientNote says when one recipient will see a message.
type recipientNote struct {
	Name     string  `json:"name"`
	Presence *string `json:"presence"`
	Delivery *string `json:"delivery"`
	Outcome  string  `json:"outcome"`
}

// Recipient outcomes, in the order aboard say lists them.
const (
	outcomeNow       = "now"
	outcomeTurnEnd   = "turn_end"
	outcomeNotWoken  = "not_woken"
	outcomeNoSession = "no_session"
	outcomePerson    = "person"
)

var outcomeOrder = []string{outcomeNow, outcomeTurnEnd, outcomeNotWoken, outcomeNoSession, outcomePerson}

// namesShown is how many names a group of recipients or senders lists before counting.
const namesShown = 5

// unreadAfterSay reads the sender's unread messages without acknowledging them. Nil if
// they couldn't be read: the note never fails the post.
func unreadAfterSay(ctx context.Context, c *client) *unreadNote {
	limit := 200
	r, err := c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Limit: &limit})
	if err != nil || r.JSON200 == nil {
		return nil
	}
	n := &unreadNote{Seqs: []int{}, From: []unreadFrom{}}
	for _, m := range r.JSON200.Messages {
		n.Count++
		n.Seqs = append(n.Seqs, m.Seq)
		f := unreadFrom{Name: m.From.Name, Sender: string(m.Sender)}
		if !slices.Contains(n.From, f) {
			n.From = append(n.From, f)
		}
	}
	return n
}

// recipientsOf works out when each member the message is addressed to will see it, from
// the presence and delivery mode the members list shows. Nil if the members couldn't be
// read.
func recipientsOf(ctx context.Context, c *client, m *api.Message) []recipientNote {
	r, err := c.api.ListMembersWithResponse(ctx, m.Board)
	if err != nil || r.JSON200 == nil {
		return nil
	}
	out := []recipientNote{}
	for _, mem := range r.JSON200.Members {
		if mem.Name == m.From.Name || !addressedTo(m.To, mem) {
			continue
		}
		n := recipientNote{Name: mem.Name, Outcome: outcomePerson}
		if mem.Kind == api.MemberKindAgent {
			if mem.Presence != nil {
				p := string(*mem.Presence)
				n.Presence = &p
			}
			if mem.Delivery != nil {
				d := string(*mem.Delivery)
				n.Delivery = &d
			}
			n.Outcome = agentOutcome(n.Presence, n.Delivery)
		}
		out = append(out, n)
	}
	return out
}

// addressedTo reports whether a message to the targets reaches member: all of them
// (no targets, or all), its name, or its role.
func addressedTo(to []api.Target, mem api.Member) bool {
	if len(to) == 0 {
		return true
	}
	for _, t := range to {
		switch {
		case t == "all", t == "@"+mem.Name:
			return true
		case mem.Role != nil && t == "role:"+*mem.Role:
			return true
		}
	}
	return false
}

// agentOutcome is when an agent sees a message from another agent: its delivery mode
// first (off, or humans, never wakes it for an agent's message), then whether a session
// is open and running a turn. An agent whose mode was never reported counts as auto.
func agentOutcome(presence, mode *string) string {
	if mode != nil && (*mode == string(delivery.ModeOff) || *mode == string(delivery.ModeHumans)) {
		return outcomeNotWoken
	}
	switch {
	case presence == nil || *presence == "no_session":
		return outcomeNoSession
	case *presence == "idle":
		return outcomeNow
	default:
		return outcomeTurnEnd
	}
}

// unreadText is the line about the sender's unread messages, or "" when there are none.
func unreadText(board string, n *unreadNote) string {
	if n == nil || n.Count == 0 {
		return ""
	}
	seqs := make([]string, 0, len(n.Seqs))
	for _, s := range n.Seqs {
		seqs = append(seqs, fmt.Sprintf("#%d", s))
	}
	return fmt.Sprintf("%d unread on %s: %s; run aboard inbox\n", n.Count, board, countedList(seqs))
}

// recipientsText is the line saying when each recipient sees the message, grouped by
// outcome, or "" when there are none.
func recipientsText(rs []recipientNote) string {
	var parts []string
	for _, o := range outcomeOrder {
		var names []string
		for _, r := range rs {
			if r.Outcome == o {
				names = append(names, "@"+r.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		one := len(names) == 1
		pick := func(single, many string) string {
			if one {
				return single
			}
			return many
		}
		list := countedList(names)
		switch o {
		case outcomeNow:
			parts = append(parts, list+pick(" gets it now.", " get it now."))
		case outcomeTurnEnd:
			parts = append(parts, list+pick(" gets it when its turn ends.", " get it when their turn ends."))
		case outcomeNotWoken:
			parts = append(parts, list+pick(" won't be woken: it sees it when it checks its inbox.",
				" won't be woken: they see it when they check their inbox."))
		case outcomeNoSession:
			parts = append(parts, list+pick(" has no open session: it sees it in its inbox or when a session resumes it.",
				" have no open session: they see it in their inbox or when a session resumes them."))
		case outcomePerson:
			parts = append(parts, list+pick(" sees it on the board or in their inbox.", " see it on the board or in their inbox."))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ") + "\n"
}

// countedList joins up to namesShown items and counts the rest.
func countedList(items []string) string {
	if len(items) <= namesShown {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:namesShown], ", ") + fmt.Sprintf(" and %d more", len(items)-namesShown)
}

// Outcomes of aboard say --wait-reply.
const (
	waitReply        = "reply"
	waitOwnerMessage = "owner_message"
	waitTimeout      = "timeout"
)

// waited is what aboard say --wait-reply saw.
type waited struct {
	outcome string
	reply   *api.Message
	owner   []api.Message
}

// waitForReply waits up to wait for a reply to sent, or for a message from the agent's
// owner, without acknowledging anything else. What it returns is acknowledged: through
// the delivery daemon when the agent is bound to a session here, so it is never handed
// to the session, else directly when nothing unread comes before it.
func (a *app) waitForReply(ctx context.Context, c *client, ref delivery.AgentRef, sent *api.Message, wait time.Duration) (waited, error) {
	held := a.holdReplies(ctx, ref, sent.Seq)
	if held != nil {
		defer func() { _ = held.Close() }()
	}
	deadline := time.Now().Add(wait)
	after := sent.Seq
	limit := 200
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return waited{outcome: waitTimeout}, nil
		}
		secs := min(int((left+time.Second-1)/time.Second), 600)
		r, err := c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{After: &after, Wait: &secs, Limit: &limit})
		if err != nil {
			return waited{}, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return waited{}, apiError(r.StatusCode(), r.Body)
		}
		var w waited
		for _, m := range r.JSON200.Messages {
			after = max(after, m.Seq)
			switch {
			case m.ReplyTo != nil && *m.ReplyTo == sent.Id && w.reply == nil:
				w.reply = &m
			case m.Sender == api.MessageSenderOwner:
				w.owner = append(w.owner, m)
			}
		}
		switch {
		case len(w.owner) > 0:
			w.outcome = waitOwnerMessage
		case w.reply != nil:
			w.outcome = waitReply
		default:
			continue
		}
		a.acknowledgeShown(ctx, c, held, w)
		return w, nil
	}
}

// holdReplies asks the delivery daemon to keep replies to seq out of the agent's
// bundles while the command waits for them, when this command runs in a harness
// session. It returns the connection that holds them, or nil.
func (a *app) holdReplies(ctx context.Context, ref delivery.AgentRef, seq int) net.Conn {
	if _, ok := a.sessionKey(); !ok {
		return nil
	}
	dctx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	conn, err := a.dialDaemon(dctx)
	if err != nil {
		return nil
	}
	req := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpHold, Agent: &ref, ReplyTo: seq}
	var resp delivery.Response
	_ = conn.SetDeadline(time.Now().Add(daemonCallTimeout))
	if delivery.WriteFrame(conn, req) != nil || delivery.ReadFrame(bufio.NewReader(conn), &resp) != nil || !resp.Held {
		_ = conn.Close()
		return nil
	}
	_ = conn.SetDeadline(time.Time{})
	return conn
}

// acknowledgeShown makes sure the messages a wait showed aren't delivered again.
func (a *app) acknowledgeShown(ctx context.Context, c *client, held net.Conn, w waited) {
	var seqs []int
	if w.reply != nil {
		seqs = append(seqs, w.reply.Seq)
	}
	for _, m := range w.owner {
		seqs = append(seqs, m.Seq)
	}
	slices.Sort(seqs)
	if held != nil {
		var resp delivery.Response
		_ = held.SetDeadline(time.Now().Add(daemonCallTimeout))
		claim := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpClaim, Seqs: seqs}
		if delivery.WriteFrame(held, claim) == nil && delivery.ReadFrame(bufio.NewReader(held), &resp) == nil && resp.Error == nil {
			return
		}
	}
	// With no daemon to record them, acknowledge them only when that moves the read
	// position past nothing else.
	limit := 200
	r, err := c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Limit: &limit})
	if err != nil || r.JSON200 == nil {
		return
	}
	last := seqs[len(seqs)-1]
	for _, m := range r.JSON200.Messages {
		if m.Seq <= last && !slices.Contains(seqs, m.Seq) {
			return
		}
	}
	_, _ = c.api.AckInboxWithResponse(ctx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: last})
}

// waitedText is the text output for what a wait saw.
func waitedText(sent *api.Message, wait int, w waited) string {
	switch w.outcome {
	case waitReply:
		return fmt.Sprintf("Reply from @%s:\n%s\n", w.reply.From.Name, deliveryText(*w.reply))
	case waitOwnerMessage:
		shown := make([]string, 0, len(w.owner)+1)
		for _, m := range w.owner {
			shown = append(shown, deliveryText(m))
		}
		text := fmt.Sprintf("Your owner sent this while you waited; a reply to #%d will reach you later:\n%s\n", sent.Seq, strings.Join(shown, "\n"))
		if w.reply != nil {
			text = fmt.Sprintf("Your owner sent this while you waited:\n%s\nReply from @%s:\n%s\n",
				strings.Join(shown, "\n"), w.reply.From.Name, deliveryText(*w.reply))
		}
		return text
	default:
		return fmt.Sprintf("Sent #%d; no reply within %d s. Don't send it again; a reply will reach you later.\n", sent.Seq, wait)
	}
}
