package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// NewMessage is a message to post.
type NewMessage struct {
	Ask          *NewAsk
	Answer       *NewAnswer
	About        *[]string
	To           []string
	Body         string
	ReplyTo      *string
	Urgent       bool
	ExpectsReply bool
}

// PostMessage stores a message on the board and wakes anyone waiting for it. It returns
// as soon as the message is stored.
func (s *Service) PostMessage(ctx context.Context, p Principal, boardName string, in NewMessage) (Message, error) {
	to := in.To
	if len(to) == 0 && in.ReplyTo == nil && in.Ask == nil {
		to = []string{rules.TargetAll}
	}
	var msg Message
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		if in.Ask != nil {
			to, err = askRecipient(tx, b, me, in)
			if err != nil {
				return err
			}
			in.ExpectsReply = true
		}
		var replyToSeq *int64
		var threadRoot *string
		if in.ReplyTo != nil {
			orig, err := tx.MessageByID(*in.ReplyTo)
			if errors.Is(err, ErrNotFound) || (err == nil && orig.BoardID != b.ID) {
				return apierr.New(http.StatusNotFound, "message_not_found", "The message you are replying to isn't on this board.",
					"Check the message id or sequence number with aboard read.")
			}
			if err != nil {
				return err
			}
			replyToSeq, threadRoot = ptr(orig.Seq), threadRootOf(orig)
			if len(to) == 0 {
				if to, err = replyRecipients(tx, b, me, orig); err != nil {
					return err
				}
			}
		}
		if to, err = checkTargets(tx, b, to); err != nil {
			return err
		}
		recipients, err := recipientsOf(tx, b, me, to)
		if err != nil {
			return err
		}
		var role rules.Role
		if me.Role != nil {
			role = b.Roles[*me.Role]
		}
		switch rules.CheckPost(b.Policy, role, me.Rules(), to, in.Urgent) {
		case rules.NeedsPost:
			return apierr.New(http.StatusForbidden, "forbidden", "Your role can't post on this board.", "Ask a human on the board to grant your role post.")
		case rules.NeedsBroadcast:
			return apierr.New(http.StatusForbidden, "broadcast_not_allowed", "You don't have permission to message everyone on this board.",
				"Use --to @name or --to role:R.")
		case rules.NeedsUrgent:
			return apierr.New(http.StatusForbidden, "urgent_not_allowed", "You don't have permission to send urgent messages on this board.",
				"Send it without --urgent; it is delivered when the recipient is idle.")
		}

		// Mentions are read from the body as stored, against the members on the board now,
		// so later joins never change who a message mentioned.
		mentions, err := resolveMentions(tx, b, me, to, in.Body)
		if err != nil {
			return err
		}
		about, err := resolveTaskTags(tx, b, me, in)
		if err != nil {
			return err
		}
		now := s.clk.Now()
		ask, err := makeAsk(tx, b, to, in.Ask, about, now)
		if err != nil {
			return err
		}
		answer, err := resolveAnswer(tx, b, me, in)
		if err != nil {
			return err
		}
		id, err := s.gen.ID("msg", now)
		if err != nil {
			return err
		}
		data := map[string]any{
			"about":      about,
			"message_id": id, "to": to, "body": in.Body, "reply_to": in.ReplyTo,
			"urgent": in.Urgent, "expects_reply": in.ExpectsReply, "redactions": []Redaction{}, "mentions": mentions,
		}
		if ask != nil {
			data["ask"] = ask
		}
		if answer != nil {
			data["answer"] = answer
		}
		if recipients != nil {
			data["recipients"] = recipients
		}
		e, err := s.append(tx, &b, events.MessagePosted, actorOf(me), now, data)
		if err != nil {
			return err
		}
		msg = Message{
			Ask: ask, Answer: answer,
			About: about,
			ID:    id, BoardID: b.ID, Seq: e.Seq, At: e.At, SenderID: me.ID, To: to, Body: in.Body, ReplyTo: in.ReplyTo,
			ReplyToSeq: replyToSeq, ThreadRoot: threadRoot, Urgent: in.Urgent, ExpectsReply: in.ExpectsReply, Redactions: []Redaction{},
			Recipients: recipients, Mentions: mentions,
			SenderName: me.Name, SenderKind: me.Kind, SenderRole: me.Role, SenderOwner: me.Owner, SenderHuman: me.HumanID,
		}
		if err := tx.InsertMessage(msg); err != nil {
			return err
		}
		// Read it back for what the store adds, such as how many people have agents here.
		if msg, err = tx.MessageByID(id); err != nil {
			return err
		}
		if err := projectAsk(tx, me, &msg, now); err != nil {
			return err
		}
		if !b.Policy.ShowHarness && me.Kind == "agent" {
			msg.SenderHarness = nil
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	s.notify.Changed(msg.BoardID)
	return msg, nil
}

// checkTargets validates a `to` list and removes duplicates.
func checkTargets(tx ReadTx, b Board, to []string) ([]string, error) {
	var out []string
	for _, t := range to {
		kind, v, ok := rules.ParseTarget(t)
		switch {
		case !ok:
			return nil, apierr.New(http.StatusUnprocessableEntity, "invalid_target",
				fmt.Sprintf("%q is not a target.", t), "Use all, @name or role:R.")
		case kind == rules.TargetAll && len(to) > 1:
			return nil, apierr.New(http.StatusUnprocessableEntity, "invalid_target",
				"all already includes everyone, so it can't be combined with other targets.", "Use --to all on its own.")
		case kind == rules.TargetName:
			if _, err := tx.MemberByName(b.ID, v); errors.Is(err, ErrNotFound) {
				return nil, apierr.New(http.StatusUnprocessableEntity, "unknown_recipient",
					fmt.Sprintf("No one on this board is called %q.", v), "Run aboard read to see who is posting here.")
			} else if err != nil {
				return nil, err
			}
		case kind == rules.TargetRole:
			if _, ok := b.Roles[v]; !ok {
				return nil, roleNotFound(v)
			}
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Reading is a page of messages for one reader.
type Reading struct {
	Work     *AgentWork
	Board    Board
	Reader   Member
	Messages []Message
	// NextAfter and PrevBefore are set when more matching messages come after or before
	// the page; Inbox leaves PrevBefore unset.
	NextAfter  *int64
	PrevBefore *int64
}

// TimelineFilter narrows a timeline read. Zero values don't filter.
type TimelineFilter struct {
	Task string
	// After and Before bound the seq window, exclusive at both ends.
	After, Before int64
	// Newest fills the page from the newest matching messages instead of the oldest.
	Newest bool
	Limit  int
	// From is the sender's member name, without @.
	From string
	// Role is the sender's role.
	Role string
	// ToMe keeps messages addressed to the caller that it didn't send.
	ToMe bool
}

// Timeline returns a page of the messages the caller may see that match f, oldest
// first. It never moves a read position.
func (s *Service) Timeline(ctx context.Context, p Principal, boardName string, f TimelineFilter) (Reading, error) {
	var r Reading
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		q := TimelineQuery{After: f.After, Before: f.Before, Newest: f.Newest, Limit: f.Limit, SenderRole: f.Role, ToMe: f.ToMe}
		if f.Task != "" {
			task, err := findTask(tx, b, f.Task)
			if err != nil {
				return err
			}
			q.TaskID = task.ID
		}
		if f.From != "" {
			sender, err := tx.MemberByName(b.ID, f.From)
			if errors.Is(err, ErrNotFound) {
				return apierr.New(http.StatusNotFound, "member_not_found", fmt.Sprintf("No one on this board is called %q.", f.From),
					"Check the name; aboard read shows who is posting here.")
			}
			if err != nil {
				return err
			}
			q.FromID = sender.ID
		}
		if _, ok := b.Roles[f.Role]; f.Role != "" && !ok {
			// A filter naming a missing role asks for something that isn't there, so
			// it is not found rather than a bad request body.
			e := roleNotFound(f.Role)
			e.Status = http.StatusNotFound
			return e
		}
		readAll := readsAll(b, me)
		msgs, err := tx.Timeline(b.ID, me, readAll, q)
		if err != nil {
			return err
		}
		if err := s.annotate(tx, b, me, msgs); err != nil {
			return err
		}
		r = Reading{Board: b, Reader: me, Messages: msgs}
		if len(msgs) == 0 {
			return nil
		}
		// More messages are looked for outside the requested window, so a reader
		// can always page on from either end.
		first, last := msgs[0].Seq, msgs[len(msgs)-1].Seq
		probe := q
		probe.After, probe.Before, probe.Newest, probe.Limit = last, 0, false, 1
		if more, err := tx.Timeline(b.ID, me, readAll, probe); err != nil {
			return err
		} else if len(more) > 0 {
			r.NextAfter = ptr(last)
		}
		probe.After, probe.Before = 0, first
		if more, err := tx.Timeline(b.ID, me, readAll, probe); err != nil {
			return err
		} else if len(more) > 0 {
			r.PrevBefore = ptr(first)
		}
		return nil
	})
	return r, err
}

// Inbox returns the agent's unread messages addressed to it, leaving out those up to
// after. If there are none and wait is positive, it waits up to wait for one to arrive.
// It never moves the cursor.
func (s *Service) Inbox(ctx context.Context, p Principal, wait time.Duration, after int64, limit int) (Reading, bool, error) {
	if p.Agent == nil {
		return Reading{}, false, apierr.AgentRequired()
	}
	deadline := s.clk.After(wait)
	for {
		var cred credentialEnd
		if wait > 0 {
			// A wait ends with the credential it was made with.
			var err error
			if cred, err = s.watchCredential(ctx, p); err != nil {
				return Reading{}, false, err
			}
		}
		changed := s.notify.Watch(p.Agent.BoardID)
		var r Reading
		var more bool
		err := s.st.Read(ctx, func(tx ReadTx) error {
			// The credential and the seat are checked in the same transaction as the read,
			// so a wait ends once the key ends or the agent or its person leaves.
			if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
				return err
			}
			b, me, err := seatOf(tx, *p.Agent)
			if err != nil {
				return err
			}
			from := me
			from.Cursor = max(me.Cursor, after)
			// A mention brings a message to the inbox only when the agent may read it.
			msgs, err := tx.Inbox(from, readsAll(b, me), limit+1)
			if err != nil {
				return err
			}
			if len(msgs) > limit {
				msgs, more = msgs[:limit], true
			}
			if err := s.annotate(tx, b, me, msgs); err != nil {
				return err
			}
			work, err := s.taskWork(tx, b, me)
			if err != nil {
				return err
			}
			r = Reading{Board: b, Reader: me, Messages: msgs, Work: &work}
			return nil
		})
		if err != nil || len(r.Messages) > 0 || wait <= 0 {
			return r, more, err
		}
		select {
		case <-changed:
		case <-cred.changed:
		case <-cred.expires:
		case <-deadline:
			return r, false, nil
		case <-ctx.Done():
			return Reading{}, false, fmt.Errorf("wait for inbox: %w", ctx.Err())
		}
	}
}

// Ack moves the agent's read position forward to upTo and returns the new position.
func (s *Service) Ack(ctx context.Context, p Principal, upTo int64) (int64, error) {
	if p.Agent == nil {
		return 0, apierr.AgentRequired()
	}
	var (
		cursor int64
		moved  bool
	)
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, _, err := seatOf(tx, *p.Agent)
		if err != nil {
			return err
		}
		if upTo > b.HeadSeq {
			return ackOutOfRange(upTo, b.HeadSeq)
		}
		before, err := tx.MemberByName(b.ID, p.Agent.Name)
		if err != nil {
			return err
		}
		if err := tx.SetCursor(p.Agent.ID, upTo); err != nil {
			return err
		}
		me, err := tx.MemberByName(b.ID, p.Agent.Name)
		cursor, moved = me.Cursor, me.Cursor != before.Cursor
		return err
	})
	if err == nil && moved {
		// Whoever acknowledged, the owner's delivery daemon follows the read position.
		s.notify.Changed(readKey(p.Agent.BoardID))
	}
	return cursor, err
}

// readKey is the Notifier key that changes when an agent's read position on the board
// moves. Like presence, a read position is bookkeeping, never an event.
func readKey(boardID string) string { return "read/" + boardID }

// Log is a page of a board's event log.
type Log struct {
	Board     Board
	Events    []events.Event
	NextAfter *int64
}

// Events returns the board's events after seq. Payloads of messages the caller may not
// read are withheld; their hashes are always included, so the chain still verifies.
func (s *Service) Events(ctx context.Context, p Principal, boardName string, after int64, limit int) (Log, error) {
	var out Log
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		evs, err := tx.Events(b.ID, after, limit)
		if err != nil {
			return err
		}
		var seqs []int64
		for _, e := range evs {
			if e.Type == events.MessagePosted {
				seqs = append(seqs, e.Seq)
			}
		}
		msgs, err := tx.MessagesBySeq(b.ID, seqs)
		if err != nil {
			return err
		}
		for i := range evs {
			m, ok := msgs[evs[i].Seq]
			// A reaction is withheld with the message it is on.
			if evs[i].Type == events.ReactionAdded || evs[i].Type == events.ReactionRemoved {
				id, err := reactionTarget(evs[i])
				if err != nil {
					return err
				}
				if m, err = tx.MessageByID(id); err != nil {
					return fmt.Errorf("message of reaction %d: %w", evs[i].Seq, err)
				}
				ok = true
			}
			if ok && !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
				evs[i].Data, evs[i].DataWithheld = nil, true
			}
		}
		out = Log{Board: b, Events: evs}
		if len(evs) == limit {
			out.NextAfter = ptr(evs[len(evs)-1].Seq)
		}
		return nil
	})
	return out, err
}
