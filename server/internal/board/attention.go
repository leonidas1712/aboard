package board

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// A member's read position on a board is how far through the board's event log they
// have acknowledged. An agent's moves with its inbox; a person's with AckBoard, which
// clients call for what they showed the person. Like presence it is bookkeeping, never
// an event, and it never moves back.

// Position is a member's read position on a board and how many messages after it are
// unread: for a person, every message after it they didn't send (people read every
// message); for an agent, its unread inbox.
type Position struct {
	ReadUpTo int64
	Unread   int64
}

// positionOf reads me's position on its board. Callers run it in the transaction that
// checked me's access.
func positionOf(tx ReadTx, me Member, mentions bool) (Position, error) {
	n, err := tx.CountUnread(me, me.Kind == "agent", mentions)
	if err != nil {
		return Position{}, fmt.Errorf("count unread for %s: %w", me.ID, err)
	}
	return Position{ReadUpTo: me.Cursor, Unread: n}, nil
}

// startReading puts a person who first joins a board, at its head: what
// was posted before is in the timeline, not unread.
func startReading(tx Tx, b Board, person Member) error {
	return tx.SetCursor(person.ID, b.HeadSeq)
}

// Acked is the caller's read position on one board after an acknowledgement.
type Acked struct {
	Board    string
	Position Position
}

// AckBoard moves the caller's read position on the board forward to upTo: a person's
// own, or an agent's on its own board, the one its inbox acknowledges. A lower upTo
// changes nothing. Whoever moved it, the person's other clients and the agent's owner's
// delivery daemon are told through the stream.
func (s *Service) AckBoard(ctx context.Context, p Principal, boardName string, upTo int64) (Acked, error) {
	var (
		out   Acked
		moved bool
		id    string
	)
	err := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		if upTo > b.HeadSeq {
			return ackOutOfRange(upTo, b.HeadSeq)
		}
		if err := tx.SetCursor(me.ID, upTo); err != nil {
			return err
		}
		moved, id = upTo > me.Cursor, b.ID
		me.Cursor = max(me.Cursor, upTo)
		pos, err := positionOf(tx, me, readsAll(b, me))
		out = Acked{Board: b.Name, Position: pos}
		return err
	})
	if err == nil && moved {
		s.notify.Changed(readKey(id))
	}
	return out, err
}

func ackOutOfRange(upTo, head int64) error {
	return apierr.New(http.StatusUnprocessableEntity, "ack_out_of_range",
		fmt.Sprintf("Sequence %d is past the end of the board (%d).", upTo, head),
		"Acknowledge up to the last sequence number you received.")
}

// recipientsOf fixes whom a message from sender to `to` reaches, as member ids: each
// member named, then each member who holds a role named, in the order they joined,
// never the sender and never anyone no longer on the board. A message to all has none:
// nil.
func recipientsOf(tx ReadTx, b Board, sender Member, to []string) ([]string, error) {
	if slices.Contains(to, rules.TargetAll) {
		return nil, nil
	}
	members, err := tx.Members(b.ID)
	if err != nil {
		return nil, err
	}
	on := present(members)
	out := []string{}
	add := func(m Member) {
		if m.ID != sender.ID && !slices.Contains(out, m.ID) {
			out = append(out, m.ID)
		}
	}
	for _, t := range to {
		kind, v, _ := rules.ParseTarget(t)
		ownerID := ""
		if kind == rules.TargetOwner {
			owner, err := tx.MemberByName(b.ID, v)
			if err != nil {
				return nil, err
			}
			ownerID = owner.HumanID
		}
		for _, m := range on {
			if (kind == rules.TargetName && m.Name == v) || (kind == rules.TargetRole && m.Role != nil && *m.Role == v) || (kind == rules.TargetOwner && m.Kind == "agent" && m.HumanID == ownerID) {
				add(m)
			}
		}
	}
	return out, nil
}

// Receipt states.
const (
	// ReceiptPending means the recipient hasn't acknowledged the message yet.
	ReceiptPending = "pending"
	// ReceiptReceived means an agent's read position has passed the message: it reached
	// the agent, through delivery or its inbox.
	ReceiptReceived = "received"
	// ReceiptRead means a person's read position has passed the message: it was shown
	// to them.
	ReceiptRead = "read"
)

// Receipt is whether a message has reached one recipient.
type Receipt struct {
	QueuedUntil string
	Member      Member
	State       string
	// Presence is an agent's presence when the receipt was read, never stored with it;
	// nil for a person.
	Presence *Presence
}

// ReceiptsReading is a message's receipts as one caller sees them. ToEveryone is true for
// a message to all, which has none.
type ReceiptsReading struct {
	Board      Board
	Message    Message
	ToEveryone bool
	Available  bool
	Recipients []Receipt
}

// Receipts says, for each recipient of the board's message seq still on the board,
// whether it has reached them, from their read positions now. Only someone on the board
// who may read the message gets them.
func (s *Service) Receipts(ctx context.Context, p Principal, boardName string, seq int64) (ReceiptsReading, error) {
	var out ReceiptsReading
	err := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, err := s.access(tx, p, boardName)
		if err != nil {
			return err
		}
		msgs, err := tx.MessagesBySeq(b.ID, []int64{seq})
		if err != nil {
			return err
		}
		m, ok := msgs[seq]
		if !ok || !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules(), m.Recipients) {
			return messageNotFound()
		}
		out = ReceiptsReading{Board: b, Message: m, ToEveryone: slices.Contains(m.To, rules.TargetAll), Available: m.Recipients != nil || slices.Contains(m.To, rules.TargetAll), Recipients: []Receipt{}}
		if out.ToEveryone || !out.Available {
			return nil
		}
		members, err := tx.Members(b.ID)
		if err != nil {
			return err
		}
		on := map[string]Member{}
		for _, x := range present(members) {
			on[x.ID] = x
		}
		now := s.clk.Now()
		for _, id := range m.Recipients {
			r, ok := on[id]
			if !ok {
				continue
			}
			rc := Receipt{Member: r, State: ReceiptPending}
			if r.Kind == "agent" {
				pr := r.CurrentPresence(now)
				rc.Presence = &pr
				rc.QueuedUntil, err = queuedReceiptExpiry(tx, r, m, now)
				if err != nil {
					return err
				}
				if r.Cursor >= m.Seq {
					rc.State = ReceiptReceived
				}
			} else if r.Cursor >= m.Seq {
				rc.State = ReceiptRead
			}
			if p.Agent != nil && !b.Policy.ShowHarness {
				rc.Member.Harness = nil
			}
			out.Recipients = append(out.Recipients, rc)
		}
		return nil
	})
	return out, err
}
