package delivery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

type sessionHandoff struct {
	manifest HandoffManifest
	parts    []offer
	digests  map[AgentKey]bool
	prefix   string
	multi    bool
}

func (s *session) restoreHandoffs(manifests []HandoffManifest) {
	if s.handoffs == nil {
		s.handoffs = map[string]*sessionHandoff{}
	}
	for _, m := range manifests {
		if m.Session == s.key {
			s.handoffs[m.ID] = &sessionHandoff{manifest: m}
		}
	}
}

func (s *session) preflightBinding(agent AgentRef) Response {
	if !multiSeatEnabled {
		return Response{V: ProtocolVersion}
	}
	for _, a := range s.agents {
		if a.ref.Server != agent.Server {
			return errorResponse("session_on_another_server", "This session already has seats on another server.", "Use a session for that server.")
		}
		if a.ref.Board != agent.Board && a.ref.MemberID == "" {
			return errorResponse("agent_not_selected", "The existing seat identity could not be verified.", "Reconnect that seat before joining another board.")
		}
		if a.ref.Board != agent.Board && s.key.Harness == "omp" && !s.ext.SupportsHandoffs() {
			return errorResponse("extension_outdated", "This extension cannot receive messages from several boards.", "Run aboard init in a terminal and restart the harness.")
		}
	}
	return Response{V: ProtocolVersion}
}

func (s *session) compositionOptions(limit int) composeOptions {
	return composeOptions{MultiSeat: len(s.agents) > 1, First: s.firstSeat, WholeLimit: limit}
}

func handoffClass(parts []offer) Class {
	count := 0
	for _, p := range parts {
		for _, m := range p.msgs {
			count++
			if !fromOwner(m) {
				return ClassMixed
			}
		}
	}
	if count == 0 {
		return ClassMixed
	}
	return ClassOwnerOnly
}

func sameAllocation(a, b []HandoffPart) bool {
	if len(a) != len(b) {
		return false
	}
	for i, p := range a {
		q := b[i]
		if p.Agent != q.Agent || p.Generation != q.Generation || !slices.Equal(p.Seqs, q.Seqs) || (q.DeliveryID != 0 && p.DeliveryID != q.DeliveryID) {
			return false
		}
	}
	return true
}

// prepare saves every admitted part before any of its text reaches the harness.
func (s *session) prepare(ctx context.Context, c composed, prefix, text string) (*sessionHandoff, []*Delivery, error) {
	for _, p := range c.parts {
		a := s.agents[p.agent.Key()]
		if a == nil || a.ref.MemberID == "" || a.generation == 0 {
			if len(s.agents) > 1 {
				return nil, nil, fmt.Errorf("a combined handoff needs verified seat identities")
			}
			deliveries := s.record(ctx, c.parts, StateHanded)
			if len(deliveries) != len(c.parts) {
				return nil, nil, fmt.Errorf("could not save legacy delivery")
			}
			h := &sessionHandoff{manifest: HandoffManifest{Session: s.key, Boot: s.boot, Class: handoffClass(c.parts)}}
			for _, dl := range deliveries {
				h.manifest.Parts = append(h.manifest.Parts, HandoffPart{Agent: dl.Agent, DeliveryID: dl.ID, Seqs: dl.Seqs})
			}
			return h, deliveries, nil
		}
	}
	manifest := HandoffManifest{Session: s.key, Boot: s.boot, Class: handoffClass(c.parts), CreatedAt: s.now()}
	hash := sha256.Sum256([]byte(text))
	manifest.PayloadHash = hex.EncodeToString(hash[:])
	for _, p := range c.parts {
		a := s.agents[p.agent.Key()]
		if a == nil || a.problem != "" || a.adopting {
			return nil, nil, fmt.Errorf("seat no longer available")
		}
		manifest.Parts = append(manifest.Parts, HandoffPart{Agent: p.agent, Generation: a.generation, Seqs: orderedSeqs(p.msgs), DeliveryID: p.redeliver})
	}
	if s.handoffs == nil {
		s.handoffs = map[string]*sessionHandoff{}
	}
	for _, h := range s.handoffs {
		old := h.manifest
		if old.Session == manifest.Session && old.Boot == manifest.Boot && old.Class == manifest.Class && old.PayloadHash == manifest.PayloadHash && sameAllocation(old.Parts, manifest.Parts) {
			manifest = old
			break
		}
	}
	if manifest.ID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, nil, err
		}
		manifest.ID = "hnd_" + hex.EncodeToString(random[:])
	}
	saved, err := s.d.cfg.Journal.PrepareHandoff(ctx, manifest)
	if err != nil {
		return nil, nil, err
	}
	h := &sessionHandoff{manifest: saved, parts: slices.Clone(c.parts), digests: c.digests, prefix: prefix, multi: len(s.agents) > 1}
	s.handoffs[saved.ID] = h
	var deliveries []*Delivery
	for _, part := range saved.Parts {
		a := s.agents[part.Agent.Key()]
		dl := a.deliveries[part.DeliveryID]
		if dl == nil {
			dl = &Delivery{ID: part.DeliveryID, Agent: part.Agent, Seqs: slices.Clone(part.Seqs), CreatedAt: saved.CreatedAt}
			a.deliveries[dl.ID] = dl
		}
		if dl.HandoffID != saved.ID {
			dl.AcceptedAt, dl.TurnStartedAt = time.Time{}, time.Time{}
			dl.Stalled = false
		}
		dl.HandoffID = saved.ID
		dl.Session, dl.Boot, dl.State, dl.UpdatedAt = s.key, s.boot, StateHanded, s.now()
		deliveries = append(deliveries, dl)
	}
	s.firstSeat = c.nextFirst
	return h, deliveries, nil
}

func orderedSeqs(messages []Message) []int {
	seqs := seqsOf(messages)
	slices.Sort(seqs)
	return slices.Compact(seqs)
}

func (s *session) confirmHandoff(ctx context.Context, h *sessionHandoff) ([]*Delivery, error) {
	m := h.manifest
	if m.ID == "" {
		var confirmed []*Delivery
		for _, p := range m.Parts {
			if a := s.agents[p.Agent.Key()]; a != nil && a.problem == "" {
				if dl := a.deliveries[p.DeliveryID]; dl != nil {
					s.setState(ctx, dl, StateConfirmed)
					if dl.State == StateConfirmed {
						confirmed = append(confirmed, dl)
					}
				}
			}
		}
		return confirmed, nil
	}
	if m.Session != s.key || m.Boot != s.boot {
		return nil, nil
	}
	var surviving []AgentKey
	for _, p := range m.Parts {
		a := s.agents[p.Agent.Key()]
		if a != nil && !a.adopting && a.problem == "" && a.generation == p.Generation {
			surviving = append(surviving, p.Agent.Key())
		}
	}
	if len(surviving) == 0 {
		return nil, nil
	}
	saved, err := s.d.cfg.Journal.ConfirmHandoff(ctx, m.ID, s.key, s.boot, surviving, s.now())
	if err != nil {
		return nil, err
	}
	var confirmed []*Delivery
	for _, dl := range saved {
		a := s.agents[dl.Agent.Key()]
		if a == nil {
			continue
		}
		p := a.deliveries[dl.ID]
		if p == nil {
			row := dl
			p = &row
			a.deliveries[dl.ID] = p
		} else {
			*p = dl
		}
		confirmed = append(confirmed, p)
	}
	return confirmed, nil
}

func (s *session) confirmManifests(ctx context.Context, before time.Time) map[int64]bool {
	covered := map[int64]bool{}
	for _, h := range s.handoffs {
		ready := false
		for _, p := range h.manifest.Parts {
			covered[p.DeliveryID] = true
			if a := s.agents[p.Agent.Key()]; a != nil {
				if dl := a.deliveries[p.DeliveryID]; dl != nil && dl.HandoffID == h.manifest.ID && dl.State == StateHanded && (before.IsZero() || dl.UpdatedAt.Before(before)) {
					ready = true
				}
			}
		}
		if !ready {
			continue
		}
		confirmed, err := s.confirmHandoff(ctx, h)
		if err != nil {
			s.d.log.Error("confirm handoff", "handoff", h.manifest.ID, "error", err)
			continue
		}
		s.logConfirmed(confirmed)
	}
	return covered
}

// composePending reuses the frozen allocation only while every part remains eligible.
// Changed bindings, modes, reads or boot produce a newly composed handoff instead.
func (s *session) composePending(offers []offer, limit int, opts composeOptions, prefix string) composed {
	available := map[AgentKey]map[int]bool{}
	for _, o := range offers {
		if available[o.agent.Key()] == nil {
			available[o.agent.Key()] = map[int]bool{}
		}
		for _, m := range o.msgs {
			available[o.agent.Key()][m.Seq] = true
		}
	}
	frozen := make([]*sessionHandoff, 0, len(s.handoffs))
	for _, h := range s.handoffs {
		frozen = append(frozen, h)
	}
	slices.SortFunc(frozen, func(a, b *sessionHandoff) int {
		if n := a.manifest.CreatedAt.Compare(b.manifest.CreatedAt); n != 0 {
			return n
		}
		return strings.Compare(a.manifest.ID, b.manifest.ID)
	})
	for _, h := range frozen {
		if h.parts == nil || h.prefix != prefix || h.multi != opts.MultiSeat || h.manifest.Boot != s.boot {
			continue
		}
		valid := true
		for _, p := range h.manifest.Parts {
			a := s.agents[p.Agent.Key()]
			if a == nil || a.generation != p.Generation || a.problem != "" {
				valid = false
				break
			}
			dl := a.deliveries[p.DeliveryID]
			if dl == nil || (dl.State != StatePending && dl.State != StateRetry && dl.State != StateHeld) {
				valid = false
				break
			}
			for _, seq := range p.Seqs {
				if !available[p.Agent.Key()][seq] {
					valid = false
				}
			}
		}
		if !valid {
			continue
		}
		parts := slices.Clone(h.parts)
		for i := range parts {
			for _, p := range h.manifest.Parts {
				if p.Agent.Key() == parts[i].agent.Key() && slices.Equal(p.Seqs, orderedSeqs(parts[i].msgs)) {
					parts[i].redeliver = p.DeliveryID
				}
			}
		}
		text := renderComposition(parts, opts.MultiSeat, h.digests)
		if len(text) > limit {
			continue
		}
		return composed{parts: parts, text: text, digests: h.digests, nextFirst: s.firstSeat}
	}
	return compose(offers, limit, opts)
}

func (s *session) textContext(agent AgentRef) deliverytext.Context {
	if len(s.agents) > 1 {
		return deliverytext.Context{Seat: agent.Name, BoardQualified: true}
	}
	return deliverytext.Context{}
}

// ensureBoot keeps validated queue sessions usable when their harness hooks were not
// trusted. A later real hook boot fences this local process marker normally.
func (s *session) ensureBoot(ctx context.Context) error {
	if s.boot != "" {
		return nil
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	boot := "boot_" + hex.EncodeToString(random[:])
	record := SessionRecord{Key: s.key, Boot: boot, Open: s.open, Process: s.proc, Lost: s.lost, Turned: s.turned, UpdatedAt: s.now()}
	if err := s.d.cfg.Journal.SaveSession(ctx, record); err != nil {
		return err
	}
	s.boot = boot
	return nil
}
