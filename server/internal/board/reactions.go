package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// A reaction is a member's emoji on a message, from a small fixed set. It is recorded as
// an event like everything else on the board, but it is not a message: it never reaches
// an inbox, so it never counts as unread and never wakes an agent.

// reactionSet is every reaction there is, in the order they are shown.
var reactionSet = []struct{ name, emoji string }{
	{"thumbsup", "👍"}, {"check", "✅"}, {"eyes", "👀"}, {"heart", "❤️"}, {"tada", "🎉"}, {"question", "❓"},
}

// emojiOf returns the emoji a reaction's name stands for.
func emojiOf(name string) (string, bool) {
	for _, r := range reactionSet {
		if r.name == name {
			return r.emoji, true
		}
	}
	return "", false
}

// ReactionCount is one emoji on a message as a reader sees it: who reacted with it,
// earliest first, and whether the reader did.
type ReactionCount struct {
	Name  string
	Emoji string
	By    []string
	Mine  bool
}

// React adds the caller's reaction to a message, or with add false takes it back, and
// returns the message as the caller then sees it. Reacting twice, or taking back a
// reaction never made, changes nothing and records nothing.
func (s *Service) React(ctx context.Context, p Principal, messageID, name string, add bool) (Reading, error) {
	emoji, ok := emojiOf(name)
	if !ok {
		return Reading{}, invalid(fmt.Sprintf("%q is not a reaction.", name), "Use one of thumbsup, check, eyes, heart, tada or question.")
	}
	var r Reading
	changed := false
	err := s.writeAs(ctx, p, func(tx Tx) error {
		m, b, me, err := s.visibleMessage(tx, p, messageID)
		if err != nil {
			return err
		}
		if err := requireActive(b); err != nil {
			return err
		}
		existing, err := tx.Reactions([]string{m.ID})
		if err != nil {
			return err
		}
		has := false
		for _, x := range existing[m.ID] {
			has = has || (x.MemberID == me.ID && x.Name == name)
		}
		if has != add {
			typ := events.ReactionAdded
			if !add {
				typ = events.ReactionRemoved
			}
			now := s.clk.Now()
			e, err := s.append(tx, &b, typ, actorOf(me), now, map[string]any{"message_id": m.ID, "name": name, "emoji": emoji})
			if err != nil {
				return err
			}
			if add {
				err = tx.InsertReaction(Reaction{MessageID: m.ID, MemberID: me.ID, Name: name, At: e.At})
			} else {
				err = tx.DeleteReaction(m.ID, me.ID, name)
			}
			if err != nil {
				return err
			}
			changed = true
		}
		msgs := []Message{m}
		if err := annotate(tx, b, me, msgs); err != nil {
			return err
		}
		r = Reading{Board: b, Reader: me, Messages: msgs}
		return nil
	})
	if err != nil {
		return Reading{}, err
	}
	if changed {
		s.notify.Changed(r.Board.ID)
	}
	return r, nil
}

// visibleMessage finds a message the caller may see, with its board and the caller's
// membership. Any message the caller may not see, on its board or another, is not found.
func (s *Service) visibleMessage(tx ReadTx, p Principal, messageID string) (Message, Board, Member, error) {
	m, err := tx.MessageByID(messageID)
	if errors.Is(err, ErrNotFound) {
		return Message{}, Board{}, Member{}, messageNotFound()
	}
	if err != nil {
		return Message{}, Board{}, Member{}, err
	}
	b, err := tx.BoardByID(m.BoardID)
	if err != nil {
		return Message{}, Board{}, Member{}, err
	}
	b, me, err := s.access(tx, p, b.Name)
	// A message on a board the caller can't read is not found, whether or not they can
	// see the board, so a message id never names its board.
	var e *apierr.Error
	if errors.As(err, &e) && (e.Code == "board_not_found" || e.Code == "not_on_board") {
		return Message{}, Board{}, Member{}, messageNotFound()
	}
	if err != nil {
		return Message{}, Board{}, Member{}, err
	}
	if !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
		return Message{}, Board{}, Member{}, messageNotFound()
	}
	return m, b, me, nil
}

// annotate fills in what each message looks like to me: how many replies the threads
// they start have, and their reactions.
func annotate(tx ReadTx, b Board, me Member, msgs []Message) error {
	if err := countReplies(tx, b, me, msgs); err != nil {
		return err
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	byMessage, err := tx.Reactions(ids)
	if err != nil {
		return fmt.Errorf("read reactions on board %s: %w", b.Name, err)
	}
	for i := range msgs {
		msgs[i].Reactions = countReactions(byMessage[msgs[i].ID], me)
	}
	return nil
}

// countReactions groups one message's reactions by emoji, in the set's order.
func countReactions(rs []Reaction, me Member) []ReactionCount {
	out := []ReactionCount{}
	for _, kind := range reactionSet {
		c := ReactionCount{Name: kind.name, Emoji: kind.emoji}
		for _, r := range rs {
			if r.Name == kind.name {
				c.By = append(c.By, r.MemberName)
				c.Mine = c.Mine || r.MemberID == me.ID
			}
		}
		if len(c.By) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// reactionTarget returns the message a reaction event is on.
func reactionTarget(e events.Event) (string, error) {
	var d struct {
		MessageID string `json:"message_id"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return "", fmt.Errorf("event %d: %w", e.Seq, err)
	}
	return d.MessageID, nil
}
