package delivery

import (
	"cmp"
	"slices"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// offer is one agent's messages offered for a bundle.
type offer struct {
	agent AgentRef
	// redeliver is the id of a delivery being handed again, all or nothing; zero for
	// new messages, which may be split across bundles.
	redeliver int64
	msgs      []Message
}

// composed is what goes into one bundle.
type composed struct {
	parts []offer
	// tooLarge are messages that don't fit in a bundle even on their own.
	tooLarge []offer
	text     string
}

// orderForBundle sorts one agent's messages as a bundle shows them: urgent ones first,
// then oldest first.
func orderForBundle(msgs []Message) {
	slices.SortStableFunc(msgs, func(a, b Message) int {
		if a.Urgent != b.Urgent {
			if a.Urgent {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
}

// compose builds a bundle of at most limit bytes from offers, taken in order. Text is
// grouped by board; a session holds one agent, so in practice that is one board's
// messages. Whatever doesn't fit is left for the next bundle; a message too
// large for any bundle is returned in tooLarge so it can be skipped.
func compose(offers []offer, limit int) composed {
	var c composed
	var groups []deliverytext.Group
	add := func(board string, msgs []Message) []deliverytext.Group {
		next := slices.Clone(groups)
		for i := range next {
			if next[i].Board == board {
				next[i].Messages = append(slices.Clone(next[i].Messages), msgs...)
				return next
			}
		}
		return append(next, deliverytext.Group{Board: board, Messages: msgs})
	}
	full := false
	for _, o := range offers {
		if full {
			break
		}
		if o.redeliver != 0 {
			next := add(o.agent.Board, o.msgs)
			if deliverytext.BundleSize(next) > limit && len(c.parts) > 0 {
				break
			}
			groups = next
			c.parts = append(c.parts, o)
			continue
		}
		taken := offer{agent: o.agent}
		for _, m := range o.msgs {
			alone := []deliverytext.Group{{Board: m.Board, Messages: []Message{m}}}
			if deliverytext.BundleSize(alone) > limit {
				c.tooLarge = append(c.tooLarge, offer{agent: o.agent, msgs: []Message{m}})
				continue
			}
			next := add(o.agent.Board, []Message{m})
			if deliverytext.BundleSize(next) > limit {
				full = true
				break
			}
			groups = next
			taken.msgs = append(taken.msgs, m)
		}
		if len(taken.msgs) > 0 {
			c.parts = append(c.parts, taken)
		}
	}
	slices.SortStableFunc(groups, func(a, b deliverytext.Group) int { return cmp.Compare(a.Board, b.Board) })
	c.text = deliverytext.Bundles(groups)
	return c
}

func seqsOf(msgs []Message) []int {
	seqs := make([]int, 0, len(msgs))
	for _, m := range msgs {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}
