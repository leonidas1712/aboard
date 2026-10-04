package delivery

import (
	"cmp"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// offer is one agent's messages offered for a bundle.
type offer struct {
	agent AgentRef
	// mode is the agent's delivery mode, which decides how the bundle shows the messages:
	// focused sets the quiet ones apart, focused and humans summarize a big backlog. Empty
	// or all shows every message in full, as does an offer of the owner's messages
	// mid-turn.
	mode Mode
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

// quietOverhead is room kept in a focused bundle for the line and the element that set
// its quiet messages apart.
const quietOverhead = 400

// compose builds a bundle of at most limit bytes from offers, taken in order. Text is
// grouped by board and agent; a session holds one agent, so in practice that is one
// board's messages. Whatever doesn't fit is left for the next bundle; a message too
// large for any bundle is returned in tooLarge so it can be skipped.
func compose(offers []offer, limit int) composed {
	var c composed
	type group struct {
		board string
		text  string
	}
	var groups []group
	used := 0
	for _, agentOffers := range byAgent(offers) {
		if used > 0 && used >= limit {
			break
		}
		room := limit - used
		if used > 0 {
			room -= 2 // the blank line between boards
		}
		parts, tooLarge, text := composeAgent(agentOffers, room, len(c.parts) > 0)
		c.tooLarge = append(c.tooLarge, tooLarge...)
		if len(parts) == 0 {
			if len(c.parts) > 0 {
				break
			}
			continue
		}
		c.parts = append(c.parts, parts...)
		groups = append(groups, group{board: agentOffers[0].agent.Board, text: text})
		if used > 0 {
			used += 2
		}
		used += len(text)
	}
	slices.SortStableFunc(groups, func(a, b group) int { return cmp.Compare(a.board, b.board) })
	texts := make([]string, 0, len(groups))
	for _, g := range groups {
		texts = append(texts, g.text)
	}
	c.text = strings.Join(texts, "\n\n")
	return c
}

// byAgent groups offers by agent, keeping their order.
func byAgent(offers []offer) [][]offer {
	var out [][]offer
	index := map[AgentRef]int{}
	for _, o := range offers {
		i, ok := index[o.agent]
		if !ok {
			i = len(out)
			index[o.agent] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], o)
	}
	return out
}

// composeAgent takes what fits in limit bytes of one agent's offers and writes it. With
// others before it in the bundle (after), a redelivery that doesn't fit waits whole.
func composeAgent(offers []offer, limit int, after bool) (parts, tooLarge []offer, text string) {
	agent, mode := offers[0].agent, offers[0].mode
	var all []Message
	for _, o := range offers {
		all = append(all, o.msgs...)
	}
	if (mode == ModeFocused || mode == ModeHumans) && digestDue(all) {
		if parts, tooLarge, text, ok := composeDigest(offers, limit); ok {
			return parts, tooLarge, text
		}
	}
	room := limit
	if mode == ModeFocused {
		room -= quietOverhead
	}
	var taken []Message
	size := func(msgs []Message) int {
		return deliverytext.BundleSize([]deliverytext.Group{{Board: agent.Board, Messages: msgs}})
	}
	full := false
	for _, o := range offers {
		if full {
			break
		}
		if o.redeliver != 0 {
			next := append(slices.Clone(taken), o.msgs...)
			if size(next) > room && (len(taken) > 0 || after) {
				break
			}
			taken = next
			parts = append(parts, o)
			continue
		}
		part := offer{agent: o.agent, mode: o.mode}
		for _, m := range o.msgs {
			if size([]Message{m}) > limit {
				tooLarge = append(tooLarge, offer{agent: o.agent, mode: o.mode, msgs: []Message{m}})
				continue
			}
			next := append(slices.Clone(taken), m)
			if size(next) > room {
				full = true
				break
			}
			taken = next
			part.msgs = append(part.msgs, m)
		}
		if len(part.msgs) > 0 {
			parts = append(parts, part)
		}
	}
	if len(taken) == 0 {
		return parts, tooLarge, ""
	}
	if mode != ModeFocused {
		orderForBundle(taken)
		return parts, tooLarge, deliverytext.Bundle(agent.Board, taken)
	}
	concern, quiet := split(taken, agent.Name)
	return parts, tooLarge, deliverytext.Woken(agent.Board, concern, quiet)
}

// digestDue reports whether a bundle of msgs is too big to show whole: more than
// DigestMessages messages, or more than DigestBytes in the delivery format.
func digestDue(msgs []Message) bool {
	if len(msgs) > DigestMessages {
		return true
	}
	return deliverytext.BundleSize([]deliverytext.Group{{Board: msgs[0].Board, Messages: msgs}}) > DigestBytes
}

// split sorts an agent's messages into those that concern it, as a bundle orders them,
// and the rest, oldest first.
func split(msgs []Message, name string) (concern, quiet []Message) {
	for _, m := range msgs {
		if Concerns(m, name) {
			concern = append(concern, m)
		} else {
			quiet = append(quiet, m)
		}
	}
	orderForBundle(concern)
	slices.SortFunc(quiet, func(a, b Message) int { return cmp.Compare(a.Seq, b.Seq) })
	return concern, quiet
}

// composeDigest writes a big backlog as a digest: the messages that concern the agent in
// full, as many as fit, and one line for each other message, which always fits. A
// redelivery is taken whole. ok is false when nothing would be summarized: then every
// message concerns the agent and the bundle shows them in full.
func composeDigest(offers []offer, limit int) (parts, tooLarge []offer, text string, ok bool) {
	agent := offers[0].agent
	var concern, quiet []Message
	for _, o := range offers {
		c, q := split(o.msgs, agent.Name)
		concern, quiet = append(concern, c...), append(quiet, q...)
	}
	if len(quiet) == 0 {
		return nil, nil, "", false
	}
	slices.SortFunc(quiet, func(a, b Message) int { return cmp.Compare(a.Seq, b.Seq) })
	orderForBundle(concern)
	inFull := map[int]bool{}
	var full []Message
	for _, o := range offers {
		if o.redeliver == 0 {
			continue
		}
		for _, m := range o.msgs {
			if Concerns(m, agent.Name) {
				inFull[m.Seq] = true
				full = append(full, m)
			}
		}
	}
	for _, m := range concern {
		if inFull[m.Seq] {
			continue
		}
		alone := deliverytext.Digest(agent.Board, []Message{m}, quiet)
		if len(alone) > limit {
			tooLarge = append(tooLarge, offer{agent: agent, mode: offers[0].mode, msgs: []Message{m}})
			continue
		}
		next := append(slices.Clone(full), m)
		orderForBundle(next)
		if len(deliverytext.Digest(agent.Board, next, quiet)) > limit {
			continue
		}
		full = next
		inFull[m.Seq] = true
	}
	for _, o := range offers {
		part := offer{agent: o.agent, mode: o.mode, redeliver: o.redeliver}
		for _, m := range o.msgs {
			if o.redeliver != 0 || inFull[m.Seq] || !Concerns(m, agent.Name) {
				part.msgs = append(part.msgs, m)
			}
		}
		if len(part.msgs) > 0 {
			parts = append(parts, part)
		}
	}
	orderForBundle(full)
	return parts, tooLarge, deliverytext.Digest(agent.Board, full, quiet), true
}

func seqsOf(msgs []Message) []int {
	seqs := make([]int, 0, len(msgs))
	for _, m := range msgs {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}
