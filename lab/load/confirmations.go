package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

type confirmation struct {
	seat    *seat
	key     messageKey
	elapsed time.Duration
	poll    time.Duration
	polled  bool
	err     error
}

type postedBoard struct {
	ready  chan struct{}
	sample sample
}

type confirmations struct {
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	results chan confirmation
}

// A post response publishes only its own board's expected identity. A pending
// later post must not consume an earlier harness's reply window.
func startConfirmations(ctx context.Context, replies <-chan polled, boards map[string]*postedBoard) *confirmations {
	ctx, cancel := context.WithCancel(ctx)
	c := &confirmations{ctx: ctx, cancel: cancel, results: make(chan confirmation, cap(replies))}
	c.workers.Go(func() {
		for range cap(replies) {
			select {
			case <-ctx.Done():
				return
			case reply := <-replies:
				c.workers.Go(func() { c.results <- confirmObserved(ctx, reply, boards[reply.seat.board.ID]) })
			}
		}
	})
	return c
}

func recordConfirmation(v confirmation, checks *deliveryCheck, polls, handovers *[]time.Duration, r *report) {
	if v.polled {
		*polls = append(*polls, v.poll)
	}
	if v.key.BoardID != "" {
		checks.Seen[v.seat.MemberID] = append(checks.Seen[v.seat.MemberID], v.key)
		*handovers = append(*handovers, v.elapsed)
		r.Deliveries++
	}
}

func (c *confirmations) collect(checks *deliveryCheck, polls, handovers *[]time.Duration, r *report) error {
	for range cap(c.results) {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case v := <-c.results:
			recordConfirmation(v, checks, polls, handovers, r)
			if v.err != nil {
				return v.err
			}
		}
	}
	return nil
}

func (c *confirmations) finish(checks *deliveryCheck, polls, handovers *[]time.Duration, r *report) {
	c.cancel()
	c.workers.Wait()
	close(c.results)
	for v := range c.results {
		recordConfirmation(v, checks, polls, handovers, r)
	}
}

func confirmObserved(ctx context.Context, reply polled, board *postedBoard) confirmation {
	result := confirmation{seat: reply.seat}
	if reply.err != nil {
		result.err = reply.err
		return result
	}
	select {
	case <-ctx.Done():
		result.err = ctx.Err()
		return result
	case <-board.ready:
	}
	want := board.sample
	messages, _ := reply.data["messages"].([]any)
	if len(messages) != 1 {
		result.err = errors.New("long poll lost or duplicated a round message")
		return result
	}
	message, _ := messages[0].(map[string]any)
	if seq(message, "seq") != want.key.Seq || str(message, "body") != want.marker {
		result.err = errors.New("long poll returned the wrong message")
		return result
	}
	result = confirmExtension(ctx, reply.seat, want)
	result.polled, result.poll = true, reply.at.Sub(want.started)
	return result
}

func confirmExtension(ctx context.Context, s *seat, sample sample) (result confirmation) {
	result.seat = s
	v, err := s.ext.next(ctx)
	if err != nil {
		result.err = err
		return result
	}
	if v.Event != "deliver" {
		result.err = errors.New("missing extension delivery")
		return result
	}
	found := markerPattern.FindAllString(v.Bundle, -1)
	if len(found) != 1 {
		result.err = errors.New("delivery lost or duplicated its marker")
		return result
	}
	if found[0] != sample.marker || sample.key.BoardID != s.board.ID {
		result.err = errors.New("delivery crossed boards or repeated an older message")
		return result
	}
	actual, err := renderedMessage(v.Bundle, s.Board)
	if err != nil {
		result.err = err
		return result
	}
	if actual != sample.key.Seq {
		result.err = errors.New("rendered message sequence disagrees with posted message")
		return result
	}
	result.key = messageKey{s.board.ID, actual}
	result.elapsed = v.at.Sub(sample.started)
	answer := map[string]any{"v": 1, "op": "received", "id": v.ID}
	if v.Handoff != "" {
		delete(answer, "id")
		answer["handoff_id"] = v.Handoff
	}
	for _, request := range []map[string]any{answer, {"v": 1, "op": "prompt"}, {"v": 1, "op": "turn_end"}} {
		if err := send(s.ext.conn, request); err != nil {
			result.err = err
			return result
		}
	}
	return result
}
