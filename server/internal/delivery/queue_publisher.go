package delivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"
)

const queueReportRenew = 15 * time.Second

type queueReportResult struct {
	agent      AgentRef
	generation uint64
	boot       string
	messages   []QueuedMessage
	stopped    bool
	err        error
}

func queueRequestKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "queue_" + hex.EncodeToString(b[:]), nil
}

func publishQueueIntent(ctx context.Context, j QueueReportJournal, srv QueueReportServer, state QueueReporter, desired []QueuedMessage) (bool, error) {
	if state.Stopped {
		return true, nil
	}
	// A retained claim is retried before considering any newer desired snapshot.
	for attempts := 0; attempts < 2; attempts++ {
		if state.Pending == nil {
			key, err := queueRequestKey()
			if err != nil {
				return false, err
			}
			in := QueueReportIntent{Session: state.Session.String(), Boot: state.Boot, IdempotencyKey: key, CredentialGeneration: state.CredentialGeneration}
			if state.Epoch == 0 {
				fence, err := srv.QueueFence(ctx, state.Agent)
				if err != nil {
					return false, err
				}
				if fence.MemberID != state.Agent.MemberID {
					return false, ErrUnauthorized
				}
				in.ExpectedEpoch = &fence.Epoch
				state.CredentialGeneration = fence.CredentialGeneration
				in.CredentialGeneration = fence.CredentialGeneration
			} else {
				in.Epoch, in.Revision = state.Epoch, state.Revision+1
				if in.Revision <= 0 {
					return false, fmt.Errorf("queue reporter revision exhausted")
				}
				in.Messages = slices.Clone(desired)
			}
			state.Pending = &in
			if err := j.SaveQueueReporter(ctx, state); err != nil {
				return false, err
			}
		}
		in := *state.Pending
		fence, err := srv.ReportQueue(ctx, state.Agent, in)
		if errors.Is(err, ErrQueueReportConflict) || errors.Is(err, ErrUnauthorized) {
			state.Stopped = true
			if saveErr := j.SaveQueueReporter(ctx, state); saveErr != nil {
				return true, saveErr
			}
			return true, err
		}
		if err != nil {
			return false, err
		}
		expected := in.Epoch
		if in.ExpectedEpoch != nil {
			expected = *in.ExpectedEpoch + 1
		}
		if fence.MemberID != state.Agent.MemberID || fence.Epoch != expected || fence.CredentialGeneration != state.CredentialGeneration || (in.ExpectedEpoch == nil && fence.Revision != in.Revision) {
			return false, fmt.Errorf("queue report response changed its fence")
		}
		state.Epoch, state.Revision = fence.Epoch, fence.Revision
		state.Pending = nil
		if err := j.SaveQueueReporter(ctx, state); err != nil {
			return false, err
		}
		// A successful claim already replaced the old report with an empty queue.
		if in.ExpectedEpoch != nil && len(desired) == 0 {
			return false, nil
		}
		if in.ExpectedEpoch == nil {
			if slices.Equal(in.Messages, desired) {
				return false, nil
			}
			// An old retained update completed; publish the newer snapshot next.
			continue
		}
	}
	return false, fmt.Errorf("queue report needs another retained step")
}

func (s *session) queueSnapshot(a *agentState) []QueuedMessage {
	out := []QueuedMessage{}
	if !s.open || (!s.working && !s.inTurn) || s.d.mode(a.ref) == ModeOff {
		return out
	}
	supported, known := s.peerBoundaryState()
	nextStep := map[string]bool{}
	if supported || !known {
		nextStep = s.peerNextCandidates(a.ref)
	}
	occupied := taken(a)
	for _, m := range a.unread {
		if fromOwner(m) || s.held(a.ref, m) || s.alreadyShown(a, m) || nextStep[m.ID] {
			continue
		}
		if _, ok := occupied[m.Seq]; ok {
			continue
		}
		if m.ID == "" || m.BoardID == "" {
			continue
		}
		out = append(out, queuedIdentity(a.ref, m))
		if len(out) == 1000 {
			break
		}
	}
	return out
}

func (s *session) publishQueues(ctx context.Context) {
	j, ok := s.d.cfg.Journal.(QueueReportJournal)
	if !ok || s.boot == "" || !s.ensureShown(ctx) {
		return
	}
	for _, a := range s.agents {
		srv, ok := s.d.server(a.ref.Server).srv.(QueueReportServer)
		if !ok || a.adopting || a.generation == 0 || !a.fetched || a.problem != "" {
			continue
		}
		if a.queueGeneration != a.generation || a.queueBoot != s.boot {
			a.queueGeneration, a.queueBoot = a.generation, s.boot
			a.queueSending, a.queueStopped, a.queueLastSet = false, false, false
			a.queueNext = time.Time{}
		}
		if a.queueSending || a.queueStopped {
			continue
		}
		desired := s.queueSnapshot(a)
		if a.queueLastSet && len(desired) == 0 && slices.Equal(a.queueLast, desired) {
			continue
		}
		if (!a.queueLastSet || slices.Equal(a.queueLast, desired)) && s.now().Before(a.queueNext) {
			continue
		}
		if len(desired) > 0 && s.now().Sub(a.queueFreshAt) > queueReportRenew {
			s.refresh(a, false)
			a.queueNext = s.now().Add(5 * time.Second)
			continue
		}
		a.queueSending = true
		ref, key, boot, generation := a.ref, s.key, s.boot, a.generation
		s.d.g.Go(func() error {
			rctx, cancel := context.WithTimeout(ctx, 2*recheckTimeout)
			defer cancel()
			state, err := j.QueueReporter(rctx, ref, key, boot, generation)
			stopped := false
			if err == nil {
				stopped, err = publishQueueIntent(rctx, j, srv, state, desired)
			}
			s.mail.put(sessionMsg{queueReport: &queueReportResult{agent: ref, generation: generation, boot: boot, messages: desired, stopped: stopped, err: err}})
			return nil
		})
	}
}

func (s *session) onQueueReport(r queueReportResult) {
	a := s.agents[r.agent.Key()]
	if a == nil || a.generation != r.generation || s.boot != r.boot {
		return
	}
	a.queueSending, a.queueStopped = false, r.stopped
	a.queueNext = s.now().Add(queueReportRenew)
	if r.err != nil {
		a.queueLastSet = false
		a.queueNext = s.now().Add(5 * time.Second)
		return
	}
	a.queueLast, a.queueLastSet = r.messages, true
	if len(r.messages) == 0 {
		a.queueNext = time.Time{}
	}
}

func (s *session) peerBoundaryState() (supported, known bool) {
	if s.ext != nil {
		return s.ext.supportsPeer, true
	}
	if s.peerHookBoot == s.boot {
		return s.peerHookSupported, true
	}
	return false, false
}

func (s *session) peerNextCandidates(ref AgentRef) map[string]bool {
	out := map[string]bool{}
	for _, o := range s.peerBoundaryOffers(true) {
		if o.agent.Key() == ref.Key() {
			for _, m := range o.msgs {
				if !fromOwner(m) {
					out[m.ID] = true
				}
			}
		}
	}
	return out
}
