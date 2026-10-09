package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

type (
	messageKey struct {
		BoardID string
		Seq     int64
	}
	observation struct {
		Key messageKey
		At  time.Time
	}
)

func firstHead(heads []observation, key messageKey) (time.Time, error) {
	var at time.Time
	for _, h := range heads {
		if h.Key.BoardID == key.BoardID && h.Key.Seq >= key.Seq && (at.IsZero() || h.At.Before(at)) {
			at = h.At
		}
	}
	if at.IsZero() {
		return at, fmt.Errorf("missing stream head for sequence %d", key.Seq)
	}
	return at, nil
}

type deliveryCheck struct {
	Expected map[string][]messageKey
	Seen     map[string][]messageKey
}

func (c deliveryCheck) validate() error {
	for seat, want := range c.Expected {
		if !slices.Equal(want, c.Seen[seat]) {
			return fmt.Errorf("missing, duplicated or out-of-order delivery for seat %s", seat)
		}
	}
	for seat, seen := range c.Seen {
		if len(seen) > 0 && len(c.Expected[seat]) == 0 {
			return errors.New("delivery reached an unexpected seat")
		}
	}
	return nil
}

type distribution struct {
	Samples int     `json:"samples"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
}

func summarize(samples []time.Duration) (distribution, error) {
	if len(samples) == 0 {
		return distribution{}, errors.New("latency distribution has no samples")
	}
	for _, sample := range samples {
		if sample < 0 {
			return distribution{}, errors.New("latency sample predates request")
		}
	}
	values := slices.Clone(samples)
	slices.Sort(values)
	quantile := func(q float64) float64 {
		return float64(values[int(math.Ceil(q*float64(len(values))))-1]) / float64(time.Millisecond)
	}
	return distribution{Samples: len(values), P50: quantile(.5), P95: quantile(.95), P99: quantile(.99), Max: quantile(1)}, nil
}
