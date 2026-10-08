package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadUsesRealServerAndDaemons(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "aboard")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// #nosec G204 -- builds a fixed repository command in a test-owned directory.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../server/cmd/aboard")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}

	report, err := run(ctx, options{Binary: binary, People: 2, Agents: 2, Boards: 2, Rounds: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Posts != 4 || report.Deliveries != 8 || report.Daemons != 2 {
		t.Fatalf("incomplete proof: %+v", report)
	}
	if report.Stream.Samples != 8 || report.LongPoll.Samples != 8 || report.Handover.Samples != 8 || report.Write.Samples != report.Posts {
		t.Fatalf("missing latency samples: %+v", report)
	}
}
