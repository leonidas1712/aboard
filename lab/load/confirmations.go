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
	err     error
}

// Each observer reads before its own harness confirms; unrelated slow observations
// must not consume that harness's reply window.
func (f *fixture) confirmRound(ctx context.Context, replies <-chan polled, boards, markers map[string]sample, checks *deliveryCheck, polls, handovers *[]time.Duration, r *report) error {
	ctx, cancel := context.WithCancel(ctx)
	results := make(chan confirmation, cap(replies))
	var workers sync.WaitGroup
	record := func(v confirmation) {
		if v.key.BoardID != "" {
			checks.Seen[v.seat.MemberID] = append(checks.Seen[v.seat.MemberID], v.key)
			*handovers = append(*handovers, v.elapsed)
			r.Deliveries++
		}
	}
	defer func() {
		cancel()
		workers.Wait()
		close(results)
		for v := range results {
			record(v)
		}
	}()
	for range cap(replies) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reply := <-replies:
			if reply.err != nil {
				return reply.err
			}
			want := boards[reply.seat.board.ID]
			messages, _ := reply.data["messages"].([]any)
			if len(messages) != 1 {
				return errors.New("long poll lost or duplicated a round message")
			}
			message, _ := messages[0].(map[string]any)
			if seq(message, "seq") != want.key.Seq || str(message, "body") != want.marker {
				return errors.New("long poll returned the wrong message")
			}
			*polls = append(*polls, reply.at.Sub(want.started))
			workers.Go(func() { results <- confirmExtension(ctx, reply.seat, markers) })
		}
	}
	for range cap(replies) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case v := <-results:
			record(v)
			if v.err != nil {
				return v.err
			}
		}
	}
	return nil
}

func confirmExtension(ctx context.Context, s *seat, markers map[string]sample) (result confirmation) {
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
	sample, ok := markers[found[0]]
	if !ok || sample.key.BoardID != s.board.ID {
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
