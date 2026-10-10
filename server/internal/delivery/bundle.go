package delivery

import (
	"cmp"
	"slices"
	"strings"
	"time"

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
	peerBoundary bool
	renderedAt   time.Time
	parts        []offer
	// tooLarge are messages that don't fit in a bundle even on their own.
	tooLarge  []offer
	text      string
	nextFirst int
	digests   map[AgentKey]bool
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

// composeOptions carries session context; fair selection advances only when the
// caller commits the admitted handoff, not when it reconstructs a retry.
type composeOptions struct {
	Now         time.Time
	MultiSeat   bool
	MultiIssuer bool
	First       int
	// WholeLimit is the payload cap before notes or other seats reserve space.
	WholeLimit int
}

// compose takes whole messages in seat order. Space used by an earlier seat can defer
// a later message, but cannot make that message oversized.
func compose(offers []offer, limit int, options ...composeOptions) composed {
	var c composed
	opts := composeOptions{WholeLimit: limit}
	if len(options) > 0 {
		opts = options[0]
		if opts.WholeLimit <= 0 {
			opts.WholeLimit = limit
		}
	}
	if offersHaveSeveralIssuers(offers) {
		opts.MultiIssuer = true
		opts.MultiSeat = true
	}
	c.renderedAt = opts.Now
	groups := byAgent(offers)
	if len(groups) == 0 {
		return c
	}
	first := ((opts.First % len(groups)) + len(groups)) % len(groups)
	c.nextFirst = (first + 1) % len(groups)
	type group struct{ board, text string }
	var rendered []group
	used := 0
	for step := 0; step < len(groups); step++ {
		agentOffers := groups[(first+step)%len(groups)]
		room := limit - used
		if used > 0 {
			room -= 2
		}
		context := deliverytext.Context{Now: opts.Now}
		if opts.MultiIssuer {
			context.Server = agentOffers[0].agent.Server
		}
		if opts.MultiSeat {
			context.Seat, context.BoardQualified = agentOffers[0].agent.Name, true
		}
		parts, tooLarge, text, digest := composeAgent(agentOffers, room, opts.WholeLimit, context)
		c.tooLarge = append(c.tooLarge, tooLarge...)
		if len(parts) == 0 {
			continue
		}
		c.parts = append(c.parts, parts...)
		if digest {
			if c.digests == nil {
				c.digests = map[AgentKey]bool{}
			}
			c.digests[agentOffers[0].agent.Key()] = true
		}
		rendered = append(rendered, group{board: agentOffers[0].agent.Board, text: text})
		if used > 0 {
			used += 2
		}
		used += len(text)
	}
	slices.SortStableFunc(rendered, func(a, b group) int { return cmp.Compare(a.board, b.board) })
	texts := make([]string, 0, len(rendered))
	for _, g := range rendered {
		texts = append(texts, g.text)
	}
	c.text = strings.Join(texts, "\n\n")
	return c
}

// renderComposition reproduces the admitted allocation without running fair selection
// or digest thresholds again. The caller keeps the original parts and digest choices
// with an in-flight handoff; a restart checks the reconstructed payload's hash.
func renderComposition(parts []offer, multiSeat bool, digests map[AgentKey]bool, renderedAt ...time.Time) string {
	multiIssuer := offersHaveSeveralIssuers(parts)
	return renderCompositionWithOptions(parts, composeOptions{MultiSeat: multiSeat || multiIssuer, MultiIssuer: multiIssuer}, digests, renderedAt...)
}

func renderCompositionWithOptions(parts []offer, opts composeOptions, digests map[AgentKey]bool, renderedAt ...time.Time) string {
	var now time.Time
	if len(renderedAt) > 0 {
		now = renderedAt[0]
	}
	type group struct{ board, text string }
	var rendered []group
	for _, groupOffers := range byAgent(parts) {
		agent, mode := groupOffers[0].agent, groupOffers[0].mode
		context := deliverytext.Context{Now: now}
		if opts.MultiIssuer {
			context.Server = agent.Server
		}
		if opts.MultiSeat {
			context.Seat, context.BoardQualified = agent.Name, true
		}
		var msgs []Message
		for _, o := range groupOffers {
			msgs = append(msgs, o.msgs...)
		}
		if len(msgs) == 0 {
			continue
		}
		var text string
		switch {
		case digests[agent.Key()]:
			concern, quiet := split(msgs, agent.Name)
			text = deliverytext.Digest(agent.Board, concern, quiet, context)
		case mode == ModeFocused:
			concern, quiet := split(msgs, agent.Name)
			text = deliverytext.Woken(agent.Board, concern, quiet, context)
		default:
			orderForBundle(msgs)
			text = deliverytext.Bundle(agent.Board, msgs, context)
		}
		rendered = append(rendered, group{board: agent.Board, text: text})
	}
	slices.SortStableFunc(rendered, func(a, b group) int { return cmp.Compare(a.board, b.board) })
	texts := make([]string, 0, len(rendered))
	for _, g := range rendered {
		texts = append(texts, g.text)
	}
	return strings.Join(texts, "\n\n")
}

// byAgent groups offers by agent, keeping their order.
func byAgent(offers []offer) [][]offer {
	var out [][]offer
	index := map[AgentKey]int{}
	for _, o := range offers {
		i, ok := index[o.agent.Key()]
		if !ok {
			i = len(out)
			index[o.agent.Key()] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], o)
	}
	return out
}

// composeAgent uses the remaining space for admission and the whole limit only to
// classify a message that cannot be delivered alone. A redelivery remains indivisible.
func composeAgent(offers []offer, limit, wholeLimit int, context deliverytext.Context) (parts, tooLarge []offer, text string, digest bool) {
	agent, mode := offers[0].agent, offers[0].mode
	var all []Message
	for _, o := range offers {
		all = append(all, o.msgs...)
	}
	if len(all) > 0 && (mode == ModeFocused || mode == ModeHumans) && digestDue(all) {
		if parts, tooLarge, text, ok := composeDigest(offers, limit, wholeLimit, context); ok {
			return parts, tooLarge, text, true
		}
	}
	render := func(msgs []Message) string {
		if mode == ModeFocused {
			concern, quiet := split(msgs, agent.Name)
			return deliverytext.Woken(agent.Board, concern, quiet, context)
		}
		ordered := slices.Clone(msgs)
		orderForBundle(ordered)
		return deliverytext.Bundle(agent.Board, ordered, context)
	}
	// Retain the existing single-seat allocation while several-seat sizing uses the
	// complete rendered wrappers and routing hints.
	legacyFocused := mode == ModeFocused && context.Seat == "" && !context.BoardQualified
	fits := func(msgs []Message) bool {
		if len(render(msgs)) > limit {
			return false
		}
		if !legacyFocused {
			return true
		}
		return deliverytext.BundleSize([]deliverytext.Group{{Board: agent.Board, Messages: msgs}}) <= limit-400
	}
	aloneSize := func(m Message) int {
		if legacyFocused {
			return len(deliverytext.Bundle(agent.Board, []Message{m}))
		}
		return len(render([]Message{m}))
	}
	var taken []Message
	full := false
	for _, o := range offers {
		if full {
			break
		}
		if o.redeliver != 0 {
			next := append(slices.Clone(taken), o.msgs...)
			if len(render(next)) > limit || (len(taken) > 0 && !fits(next)) {
				break
			}
			taken = next
			parts = append(parts, o)
			continue
		}
		part := offer{agent: o.agent, mode: o.mode}
		for _, m := range o.msgs {
			if aloneSize(m) > wholeLimit {
				tooLarge = append(tooLarge, offer{agent: o.agent, mode: o.mode, msgs: []Message{m}})
				continue
			}
			next := append(slices.Clone(taken), m)
			if !fits(next) {
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
	if len(taken) > 0 {
		text = render(taken)
	}
	return parts, tooLarge, text, false
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
func composeDigest(offers []offer, limit, wholeLimit int, context deliverytext.Context) (parts, tooLarge []offer, text string, ok bool) {
	agent := offers[0].agent
	var concern, quiet []Message
	for _, o := range offers {
		c, q := split(o.msgs, agent.Name)
		concern, quiet = append(concern, c...), append(quiet, q...)
	}
	if len(quiet) == 0 || len(deliverytext.Digest(agent.Board, nil, quiet, context)) > limit {
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
	if len(deliverytext.Digest(agent.Board, full, quiet, context)) > limit {
		return nil, nil, "", false
	}
	for _, m := range concern {
		if inFull[m.Seq] {
			continue
		}
		alone := deliverytext.Bundle(agent.Board, []Message{m}, context)
		if len(alone) > wholeLimit {
			tooLarge = append(tooLarge, offer{agent: agent, mode: offers[0].mode, msgs: []Message{m}})
			continue
		}
		next := append(slices.Clone(full), m)
		orderForBundle(next)
		if len(deliverytext.Digest(agent.Board, next, quiet, context)) > limit {
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
	return parts, tooLarge, deliverytext.Digest(agent.Board, full, quiet, context), true
}

func seqsOf(msgs []Message) []int {
	seqs := make([]int, 0, len(msgs))
	for _, m := range msgs {
		seqs = append(seqs, m.Seq)
	}
	return seqs
}

func offersHaveSeveralIssuers(offers []offer) bool {
	if len(offers) == 0 {
		return false
	}
	issuer := offers[0].agent.Server
	for _, o := range offers[1:] {
		if o.agent.Server != issuer {
			return true
		}
	}
	return false
}
