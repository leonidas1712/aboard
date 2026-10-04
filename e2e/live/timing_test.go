//go:build live

package live

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every test in the suite runs in its own lab (its own HOME, Aboard state, port, tmux
// server and scratch harness config), so every test runs in parallel with the others,
// up to go test's -parallel cap (make live sets it from LIVE_PARALLEL). A top-level test
// that didn't call t.Parallel would run alone: go test runs such tests one after
// another, each waiting for all of its subtests, before any parallel test starts.

// parallel marks t to run in parallel with the suite's other tests, and times it from
// when it starts running until its last cleanup (the lab's teardown included), for the
// table the run prints at its end. Every test that does the suite's work calls it, in
// place of t.Parallel. A test with subtests calls t.Parallel itself and leaves the
// timing to its subtests, so nothing is counted twice.
func parallel(t *testing.T) {
	t.Helper()
	t.Parallel()
	start := time.Now()
	t.Cleanup(func() {
		result := "pass"
		switch {
		case t.Failed():
			result = "fail"
		case t.Skipped():
			result = "skip"
		}
		timings.Lock()
		defer timings.Unlock()
		timings.runs = append(timings.runs, timing{name: t.Name(), took: time.Since(start), result: result})
	})
}

// timing is how long one test ran, and how it ended.
type timing struct {
	name   string
	took   time.Duration
	result string
}

// timings are this run's tests, in the order they finished.
var timings struct {
	sync.Mutex
	runs []timing
}

// printTimings writes how long each test that ran took, longest first, their sum, the
// run's wall time and the effective parallelism (the sum over the wall time), so each
// run shows what running in parallel bought. Skipped tests are counted, not listed.
func printTimings(w io.Writer, wall time.Duration) {
	timings.Lock()
	runs := slices.Clone(timings.runs)
	timings.Unlock()
	if len(runs) == 0 {
		return
	}
	slices.SortStableFunc(runs, func(a, b timing) int { return cmp.Compare(b.took, a.took) })
	var sum time.Duration
	var skipped int
	var b strings.Builder
	limit := "?"
	if f := flag.Lookup("test.parallel"); f != nil {
		limit = f.Value.String()
	}
	fmt.Fprintf(&b, "\nlive: how long each test took (up to %s at once)\n", limit)
	for _, r := range runs {
		if r.result == "skip" {
			skipped++
			continue
		}
		sum += r.took
		fmt.Fprintf(&b, "  %7.1fs  %-4s  %s\n", r.took.Seconds(), r.result, r.name)
	}
	fmt.Fprintf(&b, "  %d tests (%d skipped): sum %s, wall %s, effective parallelism %.1fx\n",
		len(runs)-skipped, skipped, sum.Round(time.Second), wall.Round(time.Second), sum.Seconds()/wall.Seconds())
	_, _ = io.WriteString(w, b.String())
}
