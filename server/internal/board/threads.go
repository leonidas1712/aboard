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
		var cred credentialEnd
		if wait > 0 {
			// A wait ends with the credential it was made with.
			var err error
			if cred, err = s.watchCredential(ctx, p); err != nil {
				return ThreadReading{}, err
			}
		}
		var r ThreadReading
		var boardID string
		err := s.st.Read(ctx, func(tx ReadTx) error {
			// Replies are read with the credential checked in the same transaction, so a
			// wait whose key ended meanwhile reads nothing new.
			if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
				return err
			}
			m, b, me, err := visibleMessage(tx, p, messageID)
			if err != nil {
				return err
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
			if r.Root != nil {
				replies = append([]Message{*r.Root}, replies...)
			}
			if err := annotate(tx, b, me, replies); err != nil {
				return err
			}
			if r.Root != nil {
				r.Root, replies = &replies[0], replies[1:]
			}
			r.Replies = replies
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
		case <-cred.changed:
		case <-cred.expires:
		case <-deadline:
			return r, nil
		case <-ctx.Done():
			return ThreadReading{}, fmt.Errorf("wait for replies: %w", ctx.Err())
		}
	}
}

// ThreadList is a board's threads as one reader sees them.
type ThreadList struct {
	Board   Board
	Reader  Member
	Threads []ThreadEntry
	// More is set when limit cut the list short.
	More bool
}

// ThreadEntry is one thread in a ThreadList: its first message, with its replies
// counted, and who wrote in it.
type ThreadEntry struct {
	Root Message
	// Participants are the first message's sender, then each replier in the order they
	// first replied.
	Participants []string
}

// Threads returns up to limit of the board's threads that the caller may see, the one
// with the newest reply first.
func (s *Service) Threads(ctx context.Context, p Principal, boardName string, limit int) (ThreadList, error) {
	var out ThreadList
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, err := access(tx, p, boardName)
		if err != nil {
			return err
		}
		infos, err := tx.Threads(b.ID, me, readsAll(b, me), limit+1)
		if err != nil {
			return err
		}
		out = ThreadList{Board: b, Reader: me, More: len(infos) > limit}
		infos = infos[:min(len(infos), limit)]
		roots := make([]Message, 0, len(infos))
		for _, info := range infos {
			root, err := tx.MessageByID(info.RootID)
			if err != nil {
				return fmt.Errorf("thread root %s: %w", info.RootID, err)
			}
			roots = append(roots, root)
		}
		if err := annotate(tx, b, me, roots); err != nil {
			return err
		}
		for i, root := range roots {
			who := []string{root.SenderName}
			for _, name := range infos[i].Repliers {
				if !slices.Contains(who, name) {
					who = append(who, name)
				}
			}
			out.Threads = append(out.Threads, ThreadEntry{Root: root, Participants: who})
		}
		return nil
	})
	return out, err
}
