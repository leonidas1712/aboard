package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

type writerReport struct {
	Writers      int          `json:"writers"`
	Posts        int          `json:"posts"`
	Verified     int          `json:"verified"`
	PeakInFlight int64        `json:"peak_in_flight"`
	Seconds      float64      `json:"seconds"`
	Throughput   float64      `json:"successful_posts_per_second"`
	Throttles    int64        `json:"throttled_requests"`
	Write        distribution `json:"request_to_write_response"`
}

type writeReceipt struct {
	seat     *seat
	id, body string
	seq      int64
}
type writerResult struct {
	receipts  []writeReceipt
	latencies []time.Duration
	err       error
}

func moreRounds(done, minimum int, elapsed, soak time.Duration) bool {
	return done < minimum || elapsed < soak
}

func (f *fixture) concurrentWrites(ctx context.Context, count, each int) (*writerReport, error) {
	var seats []*seat
	for _, p := range f.people {
		seats = append(seats, p.seats...)
	}
	count = min(count, len(seats))
	r := &writerReport{Writers: count}
	if count < 1 {
		return r, errors.New("no agent writers")
	}
	ready, start := make(chan struct{}, count), make(chan struct{})
	results := make(chan writerResult, count)
	var active, peak atomic.Int64
	for i := range count {
		s := seats[i*len(seats)/count]
		go func() {
			ready <- struct{}{}
			<-start
			var result writerResult
			var last int64
			for j := range each {
				body := fmt.Sprintf("load-writer-%d-message-%d", i, j)
				at := time.Now()
				n := active.Add(1)
				for before := peak.Load(); n > before && !peak.CompareAndSwap(before, n); before = peak.Load() {
				}
				data, err := f.api(ctx, "POST", "/v1/boards/"+s.Board+"/messages", s.Token, map[string]any{"body": body, "to": []string{"@" + s.owner.handle}})
				active.Add(-1)
				if err != nil {
					result.err = err
					break
				}
				sequence, id := seq(data, "seq"), str(data, "id")
				if sequence <= last || id == "" {
					result.err = errors.New("writer received an invalid or out-of-order receipt")
					break
				}
				last = sequence
				result.latencies = append(result.latencies, time.Since(at))
				result.receipts = append(result.receipts, writeReceipt{s, id, body, sequence})
			}
			results <- result
		}()
	}
	for range count {
		<-ready
	}
	throttles := f.throttles.Load()
	at := time.Now()
	close(start)
	var receipts []writeReceipt
	var latencies []time.Duration
	var first error
	for range count {
		result := <-results
		receipts = append(receipts, result.receipts...)
		latencies = append(latencies, result.latencies...)
		if first == nil {
			first = result.err
		}
	}
	r.Seconds = time.Since(at).Seconds()
	r.Posts = len(receipts)
	r.PeakInFlight = peak.Load()
	r.Throttles = f.throttles.Load() - throttles
	r.Throughput = float64(r.Posts) / r.Seconds
	if len(latencies) > 0 {
		r.Write, _ = summarize(latencies)
	}
	if first != nil {
		return r, first
	}
	seen := map[messageKey]bool{}
	ids := map[string]bool{}
	for _, receipt := range receipts {
		key := messageKey{receipt.seat.board.ID, receipt.seq}
		if seen[key] || ids[receipt.id] {
			return r, errors.New("writer receipts duplicate a message")
		}
		seen[key], ids[receipt.id] = true, true
		page, err := f.api(ctx, "GET", fmt.Sprintf("/v1/boards/%s/messages?after=%d&before=%d&limit=1", receipt.seat.Board, receipt.seq-1, receipt.seq+1), receipt.seat.Token, nil)
		if err != nil {
			return r, err
		}
		messages, _ := page["messages"].([]any)
		if len(messages) != 1 {
			return r, errors.New("writer read-back is missing its message")
		}
		message, _ := messages[0].(map[string]any)
		if seq(message, "seq") != receipt.seq || str(message, "id") != receipt.id || str(message, "body") != receipt.body || str(obj(message, "from"), "name") != receipt.seat.Name || str(message, "sender") != "self" {
			return r, errors.New("writer read-back disagrees with its receipt")
		}
		r.Verified++
	}
	if r.Posts != count*each {
		return r, errors.New("writer phase lost a message")
	}
	return r, nil
}
