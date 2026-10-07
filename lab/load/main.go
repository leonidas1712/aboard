// Command load drives a real Aboard server and delivery daemons without models.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type (
	options struct {
		Binary                         string
		People, Agents, Boards, Rounds int
	}
	report struct {
		Status         string              `json:"status"`
		Stage          string              `json:"failed_stage,omitempty"`
		Processes      []processDiagnostic `json:"processes,omitempty"`
		People         int                 `json:"people"`
		Agents         int                 `json:"agents_per_person"`
		Boards         int                 `json:"boards"`
		Daemons        int                 `json:"daemons"`
		Posts          int                 `json:"posts"`
		Deliveries     int                 `json:"deliveries"`
		VerifiedChains int                 `json:"verified_chains"`
		Throttles      int                 `json:"throttled_requests"`
		Setup          float64             `json:"setup_seconds"`
		Measurement    float64             `json:"measurement_seconds"`
		Throughput     float64             `json:"successful_posts_per_second"`
		Stream         distribution        `json:"request_to_stream"`
		LongPoll       distribution        `json:"request_to_long_poll"`
		Handover       distribution        `json:"request_to_handover"`
	}
)

func main() { os.Exit(execute()) }

func execute() int {
	var o options
	flag.StringVar(&o.Binary, "binary", "", "existing aboard binary; otherwise build it")
	flag.IntVar(&o.People, "people", 50, "people, each with an isolated home and daemon")
	flag.IntVar(&o.Agents, "agents", 10, "agent seats per person")
	flag.IntVar(&o.Boards, "boards", 20, "boards")
	flag.IntVar(&o.Rounds, "rounds", 3, "posting rounds")
	limit := flag.Duration("timeout", 30*time.Minute, "whole-run deadline, including provisioning")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *limit)
	defer cancel()
	result, err := run(ctx, o)
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		fmt.Fprintln(os.Stderr, encodeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if result.Stream.P99 >= 100 {
		fmt.Fprintln(os.Stderr, "request-to-stream p99 exceeds the 100 ms conservative target")
		return 1
	}

	return 0
}
