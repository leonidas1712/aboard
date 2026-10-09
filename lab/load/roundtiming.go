package main

import (
	"context"
	"sync"
	"time"
)

type roundTiming struct {
	Round           int     `json:"round"`
	Observers       float64 `json:"observer_preparation_seconds"`
	Delivery        float64 `json:"delivery_seconds"`
	Acknowledgments float64 `json:"acknowledgment_verification_seconds"`
	FailedPhase     string  `json:"failed_phase,omitempty"`
}

type roundClock struct {
	report *report
	index  int
	phase  string
	start  time.Time
}

func newRoundTiming(r *report, round int) *roundClock {
	r.RoundPhases = append(r.RoundPhases, roundTiming{Round: round})
	return &roundClock{report: r, index: len(r.RoundPhases) - 1, phase: "observers", start: time.Now()}
}

func (c *roundClock) next(phase string) {
	elapsed := time.Since(c.start).Seconds()
	r := &c.report.RoundPhases[c.index]
	switch c.phase {
	case "observers":
		r.Observers = elapsed
	case "delivery":
		r.Delivery = elapsed
	case "acknowledgments":
		r.Acknowledgments = elapsed
	}
	c.phase, c.start = phase, time.Now()
}

func (c *roundClock) finish(err error) {
	if err != nil {
		c.report.RoundPhases[c.index].FailedPhase = c.phase
	}
	c.next("")
}

func (f *fixture) verifyAcks(ctx context.Context, want func(*seat) int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	slots := make(chan struct{}, 16)
	var workers sync.WaitGroup
	var failed sync.Once
	var result error
launch:
	for _, person := range f.people {
		for _, s := range person.seats {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				break launch
			}
			workers.Go(func() {
				defer func() { <-slots }()
				if err := f.awaitAck(ctx, s, want(s)); err != nil {
					failed.Do(func() { result = err; cancel() })
				}
			})
		}
	}
	workers.Wait()
	if result != nil {
		return result
	}
	return ctx.Err()
}
