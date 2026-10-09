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
		Binary, Server, AdminKeyFile   string
		People, Agents, Boards, Rounds int
		Writers, WritesPerWriter       int
		Soak                           time.Duration
	}
	report struct {
		Status            string              `json:"status"`
		Stage             string              `json:"failed_stage,omitempty"`
		Processes         []processDiagnostic `json:"processes,omitempty"`
		People            int                 `json:"people"`
		Agents            int                 `json:"agents_per_person"`
		Boards            int                 `json:"boards"`
		Daemons           int                 `json:"daemons"`
		Posts             int                 `json:"posts"`
		Deliveries        int                 `json:"deliveries"`
		VerifiedChains    int                 `json:"verified_chains"`
		Throttles         int                 `json:"throttled_requests"`
		Setup             float64             `json:"setup_seconds"`
		Measurement       float64             `json:"measurement_seconds"`
		Throughput        float64             `json:"successful_posts_per_second"`
		Write             distribution        `json:"request_to_write_response"`
		Stream            distribution        `json:"request_to_stream"`
		LongPoll          distribution        `json:"request_to_long_poll"`
		Handover          distribution        `json:"request_to_handover"`
		Rounds            int                 `json:"completed_rounds"`
		ConfiguredStreams int                 `json:"configured_sse_streams"`
		Resources         resourceHistory     `json:"resources"`
		Concurrent        *writerReport       `json:"concurrent_writers,omitempty"`
	}
)

func main() { os.Exit(execute()) }

func execute() int {
	var o options
	flag.StringVar(&o.Binary, "binary", "", "existing aboard binary; otherwise build it")
	flag.StringVar(&o.Server, "server", "", "origin of a disposable remote server; requires --admin-key-file")
	flag.StringVar(&o.AdminKeyFile, "admin-key-file", "", "private file containing that remote server admin key")
	flag.IntVar(&o.People, "people", 50, "people, each with an isolated home and daemon")
	flag.IntVar(&o.Agents, "agents", 10, "agent seats per person")
	flag.IntVar(&o.Boards, "boards", 20, "boards")
	flag.IntVar(&o.Rounds, "rounds", 3, "posting rounds")
	flag.IntVar(&o.Writers, "writers", 16, "concurrent agent writers after delivery verification; zero disables")
	flag.IntVar(&o.WritesPerWriter, "writes-per-writer", 10, "messages per concurrent writer")
	flag.DurationVar(&o.Soak, "soak", 0, "minimum delivery measurement duration, with at least --rounds rounds")
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
