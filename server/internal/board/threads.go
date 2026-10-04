package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// A thread is a message that replies to nothing and every reply under it: a reply joins
// the thread of the message it answers, so replies to replies stay one level deep. The
// store keeps each reply's thread root as a read model, set when the reply is posted.

// threadRootOf returns the thread a reply to orig belongs to.
func threadRootOf(orig Message) *string {
	if orig.ThreadRoot != nil {
		return orig.ThreadRoot
	}
	return ptr(orig.ID)
}

// readsAll reports whether me may read every message on b, which a person always may.
func readsAll(b Board, me Member) bool {
	return b.Policy.Visibility == rules.VisibilityOpen || me.Kind == "human"
}

// countReplies fills ReplyCount and LastReplyAt on the messages that start a thread,
// counting only the replies me may see.
func countReplies(tx ReadTx, b Board, me Member, msgs []Message) error {
	var roots []string
	for _, m := range msgs {
		if m.ThreadRoot == nil {
			roots = append(roots, m.ID)
		}
	}
	counts, err := tx.ThreadCounts(me, readsAll(b, me), roots)
	if err != nil {
		return fmt.Errorf("count replies on board %s: %w", b.Name, err)
	}
	for i := range msgs {
		if c, ok := counts[msgs[i].ID]; ok {
			msgs[i].ReplyCount, msgs[i].LastReplyAt = c.Replies, ptr(c.LastAt)
		}
	}
	return nil
}

// threadScanLimit bounds how many replies replyRecipients reads from one thread.
const threadScanLimit = 1000

// replyRecipients is who a reply to orig goes to when it names no one: the author
// of every message in orig's thread that me may see, orig's author first, and every
// member such a message was addressed to by name, in the order they first appear,
// leaving out me and anyone no longer on the board. It never returns all; with no one
// else in the thread it fails with reply_has_no_recipients.
func replyRecipients(tx ReadTx, b Board, me Member, orig Message) ([]string, error) {
	rootID := orig.ID
	var thread []Message
	if rules.CanRead(b.Policy, orig.To, orig.SenderID, me.Rules()) {
		thread = append(thread, orig)
	}
	if orig.ThreadRoot != nil {
		rootID = *orig.ThreadRoot
		root, err := tx.MessageByID(rootID)
		if err != nil {
			return nil, fmt.Errorf("thread root %s: %w", rootID, err)
		}
		if rules.CanRead(b.Policy, root.To, root.SenderID, me.Rules()) {
			thread = append(thread, root)
		}
	}
	replies, err := tx.Thread(rootID, me, readsAll(b, me), 0, threadScanLimit)
	if err != nil {
		return nil, err
	}
	thread = append(thread, replies...)
	var to []string
	add := func(name string) error {
		target := "@" + name
		if name == me.Name || slices.Contains(to, target) {
			return nil
		}
		if _, err := tx.MemberByName(b.ID, name); errors.Is(err, ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		to = append(to, target)
		return nil
	}
	for _, m := range thread {
		if err := add(m.SenderName); err != nil {
			return nil, err
		}
		for _, t := range m.To {
			if kind, name, ok := rules.ParseTarget(t); ok && kind == rules.TargetName {
				if err := add(name); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(to) == 0 {
		return nil, apierr.New(http.StatusUnprocessableEntity, "reply_has_no_recipients",
			"No one else is in this thread, so the reply has no one to go to.",
			"Address it: --to all, --to @name or --to role:R.")
	}
	return to, nil
}

// ThreadReading is one thread as its reader sees it.
type ThreadReading struct {
	Board  Board
	Reader Member
	// Root is the thread's first message; nil when the reader may not see it.
	Root    *Message
	Replies []Message
	// NextAfter is set when more replies the reader may see come after the page.
	NextAfter *int64
}

func messageNotFound() error {
	return apierr.New(http.StatusNotFound, "message_not_found", "There is no such message that you can see.",
		"Check the message id or sequence number with aboard read.")
}

// Thread returns the thread messageID belongs to: its first message and the replies
// after after, oldest first, that the caller may see. messageID may be any message in
// the thread. If there are no replies and wait is positive, it waits up to wait for one.
func (s *Service) Thread(ctx context.Context, p Principal, messageID string, wait time.Duration, after int64, limit int) (ThreadReading, error) {
	deadline := s.clk.After(wait)
	for {
		var r ThreadReading
		var boardID string
		err := s.st.Read(ctx, func(tx ReadTx) error {
			m, err := tx.MessageByID(messageID)
			if errors.Is(err, ErrNotFound) {
				return messageNotFound()
			}
			if err != nil {
				return err
			}
			b, err := tx.BoardByID(m.BoardID)
			if err != nil {
				return err
			}
			b, me, err := access(tx, p, b.Name)
			var e *apierr.Error
			if errors.As(err, &e) && e.Code == "board_not_found" {
				return messageNotFound()
			}
			if err != nil {
				return err
			}
			if !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
				return messageNotFound()
			}
			boardID = b.ID
			r = ThreadReading{Board: b, Reader: me}
			rootID := m.ID
			if m.ThreadRoot != nil {
				rootID = *m.ThreadRoot
				root, err := tx.MessageByID(rootID)
				if err != nil {
					return fmt.Errorf("thread root %s: %w", rootID, err)
				}
				m = root
			}
			if rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
				r.Root = &m
			}
			replies, err := tx.Thread(rootID, me, readsAll(b, me), after, limit+1)
			if err != nil {
				return err
			}
			if len(replies) > limit {
				replies = replies[:limit]
				r.NextAfter = ptr(replies[limit-1].Seq)
			}
			r.Replies = replies
			if r.Root != nil {
				roots := []Message{*r.Root}
				if err := countReplies(tx, b, me, roots); err != nil {
					return err
				}
				r.Root = &roots[0]
			}
			return nil
		})
		if err != nil || len(r.Replies) > 0 || wait <= 0 {
			return r, err
		}
		changed := s.notify.Watch(boardID)
		// Read again before waiting, so a reply posted between the read and the watch
		// isn't missed.
		if again, err := s.Thread(ctx, p, messageID, 0, after, limit); err != nil || len(again.Replies) > 0 {
			return again, err
		}
		select {
		case <-changed:
		case <-deadline:
			return r, nil
		case <-ctx.Done():
			return ThreadReading{}, fmt.Errorf("wait for replies: %w", ctx.Err())
		}
	}
}
