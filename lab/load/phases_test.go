package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTheRealLoadCommandReportsWritersSoakAndResourceHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "./lab/load", "--people", "1", "--agents", "2", "--boards", "2", "--rounds", "2", "--writers", "2", "--writes-per-writer", "2", "--soak", "1ms")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = cleanEnv(t.TempDir(), "127.0.0.1:0")
	if os.Getenv("GOMODCACHE") == "" {
		cache, err := exec.CommandContext(ctx, "go", "env", "GOMODCACHE").Output()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "GOMODCACHE="+string(bytes.TrimSpace(cache)))
	}
	stdout, err := cmd.Output()
	if err != nil && len(stdout) == 0 {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("load command failed before reporting: %s", exit.Stderr)
		}
		t.Fatal(err)
	}
	var result struct {
		Status     string `json:"status"`
		Rounds     int    `json:"completed_rounds"`
		Deliveries int    `json:"deliveries"`
		Chains     int    `json:"verified_chains"`
		Writers    struct {
			Posts, Verified int
			Samples         struct{ Samples int } `json:"request_to_write_response"`
		} `json:"concurrent_writers"`
		Resources struct {
			Samples []struct {
				Seconds float64
				RSS     int64 `json:"rss_bytes"`
			}
		} `json:"resources"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" || result.Rounds != 2 || result.Deliveries != 4 || result.Chains != 2 || result.Writers.Posts != 4 || result.Writers.Verified != 4 || result.Writers.Samples.Samples != 4 {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Logf("load failure: %s", exit.Stderr)
		}
		t.Fatalf("missing phase correctness: %+v", result)
	}
	if len(result.Resources.Samples) < 2 {
		t.Fatal("resource history has no interval")
	}
	for i, sample := range result.Resources.Samples {
		if sample.RSS <= 0 || (i > 0 && sample.Seconds <= result.Resources.Samples[i-1].Seconds) {
			t.Fatal("invalid resource history")
		}
	}
}
