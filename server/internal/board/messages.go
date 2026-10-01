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
	"github.com/leonidas1712/aboard/server/internal/store"
)

// NewMessage is a message to post.
type NewMessage struct {
	To           []string
	Body         string
	ReplyTo      *string
	Urgent       bool
	ExpectsReply bool
}

// PostMessage stores a message on the board and wakes anyone waiting for it. It returns
// as soon as the message is stored.
func (s *Service) PostMessage(ctx context.Context, p Principal, boardName string, in NewMessage) (store.Message, error) {
	to := in.To
	if len(to) == 0 {
		to = []string{rules.TargetAll}
	}
	var msg store.Message
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		b, me, err := access(tx, p, boardName)
		if err != nil {
			return err
		}
		if to, err = checkTargets(tx, b, to); err != nil {
			return err
		}
		var replyToSeq *int64
		if in.ReplyTo != nil {
			orig, err := tx.MessageByID(*in.ReplyTo)
			if errors.Is(err, store.ErrNotFound) || (err == nil && orig.BoardID != b.ID) {
				return apierr.New(http.StatusNotFound, "message_not_found", "The message you are replying to isn't on this board.",
					"Check the message id or sequence number with aboard read.")
			}
			if err != nil {
				return err
			}
			replyToSeq = ptr(orig.Seq)
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

		now := s.clk.Now()
		id, err := s.gen.ID("msg", now)
		if err != nil {
			return err
		}
		e, err := s.append(tx, &b, events.MessagePosted, actorOf(me), now, map[string]any{
			"message_id": id, "to": to, "body": in.Body, "reply_to": in.ReplyTo,
			"urgent": in.Urgent, "expects_reply": in.ExpectsReply, "redactions": []store.Redaction{},
		})
		if err != nil {
			return err
		}
		msg = store.Message{
			ID: id, BoardID: b.ID, Seq: e.Seq, At: e.At, SenderID: me.ID, To: to, Body: in.Body, ReplyTo: in.ReplyTo,
			ReplyToSeq: replyToSeq, Urgent: in.Urgent, ExpectsReply: in.ExpectsReply, Redactions: []store.Redaction{},
			SenderName: me.Name, SenderKind: me.Kind, SenderRole: me.Role, SenderOwner: me.Owner, SenderHuman: me.HumanID,
		}
		return tx.InsertMessage(msg)
	})
	if err != nil {
		return store.Message{}, err
	}
	s.notify.changed(msg.BoardID)
	return msg, nil
}

// checkTargets validates a `to` list and removes duplicates.
func checkTargets(tx *store.Tx, b store.Board, to []string) ([]string, error) {
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
			if _, err := tx.MemberByName(b.ID, v); errors.Is(err, store.ErrNotFound) {
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
	Board     store.Board
	Reader    store.Member
	Messages  []store.Message
	NextAfter *int64
}

// Timeline returns the messages the caller may see after seq. It never moves a cursor.
func (s *Service) Timeline(ctx context.Context, p Principal, boardName string, after int64, limit int) (Reading, error) {
	var r Reading
	err := s.st.Read(ctx, func(tx *store.Tx) error {
		b, me, err := access(tx, p, boardName)
		if err != nil {
			return err
		}
		readAll := b.Policy.Visibility == rules.VisibilityOpen || me.Kind == "human"
		msgs, err := tx.Timeline(b.ID, me, readAll, after, limit)
		if err != nil {
			return err
		}
		r = Reading{Board: b, Reader: me, Messages: msgs}
		if len(msgs) == limit {
			r.NextAfter = ptr(msgs[len(msgs)-1].Seq)
		}
		return nil
	})
	return r, err
}

// Inbox returns the agent's unread messages addressed to it. If there are none and wait
// is positive, it waits up to wait for one to arrive. It never moves the cursor.
func (s *Service) Inbox(ctx context.Context, p Principal, wait time.Duration, limit int) (Reading, bool, error) {
	if p.Agent == nil {
		return Reading{}, false, apierr.AgentRequired()
	}
	deadline := s.clk.After(wait)
	for {
		changed := s.notify.watch(p.Agent.BoardID)
		var r Reading
		var more bool
		err := s.st.Read(ctx, func(tx *store.Tx) error {
			b, err := tx.BoardByID(p.Agent.BoardID)
			if err != nil {
				return err
			}
			me, err := tx.MemberByName(b.ID, p.Agent.Name)
			if err != nil {
				return err
			}
			msgs, err := tx.Inbox(me, limit+1)
			if err != nil {
				return err
			}
			if len(msgs) > limit {
				msgs, more = msgs[:limit], true
			}
			r = Reading{Board: b, Reader: me, Messages: msgs}
			return nil
		})
		if err != nil || len(r.Messages) > 0 || wait <= 0 {
			return r, more, err
		}
		select {
		case <-changed:
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
	var cursor int64
	err := s.st.Tx(ctx, func(tx *store.Tx) error {
		b, err := tx.BoardByID(p.Agent.BoardID)
		if err != nil {
			return err
		}
		if upTo > b.HeadSeq {
			return apierr.New(http.StatusUnprocessableEntity, "ack_out_of_range",
				fmt.Sprintf("Sequence %d is past the end of the board (%d).", upTo, b.HeadSeq),
				"Acknowledge up to the last sequence number you received.")
		}
		if err := tx.SetCursor(p.Agent.ID, upTo); err != nil {
			return err
		}
		me, err := tx.MemberByName(b.ID, p.Agent.Name)
		cursor = me.Cursor
		return err
	})
	return cursor, err
}

// Log is a page of a board's event log.
type Log struct {
	Board     store.Board
	Events    []events.Event
	NextAfter *int64
}

// Events returns the board's events after seq. Payloads of messages the caller may not
// read are withheld; their hashes are always included, so the chain still verifies.
func (s *Service) Events(ctx context.Context, p Principal, boardName string, after int64, limit int) (Log, error) {
	var out Log
	err := s.st.Read(ctx, func(tx *store.Tx) error {
		b, me, err := access(tx, p, boardName)
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
			if m, ok := msgs[evs[i].Seq]; ok && !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
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
