package main

import (
	"context"
	"errors"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type resourceSample struct {
	Seconds float64  `json:"seconds"`
	Phase   string   `json:"phase"`
	RSS     int64    `json:"rss_bytes"`
	CPU     *float64 `json:"cpu_percent"`
}

type resourceHistory struct {
	Available bool             `json:"available"`
	Samples   []resourceSample `json:"samples"`
	Errors    int              `json:"sampling_errors"`
}

type resourceMonitor struct {
	phase           atomic.Pointer[string]
	cancel          context.CancelFunc
	done            chan struct{}
	history         resourceHistory
	pid             int
	start, previous time.Time
	previousCPU     float64
}

func startResources(ctx context.Context, pid int) *resourceMonitor {
	ctx, cancel := context.WithCancel(ctx)
	m := &resourceMonitor{cancel: cancel, done: make(chan struct{}), pid: pid, start: time.Now()}
	m.setPhase("setup")
	go func() {
		defer close(m.done)
		m.capture(ctx)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.capture(ctx)
			}
		}
	}()
	return m
}

func (m *resourceMonitor) stop(ctx context.Context) resourceHistory {
	m.cancel()
	<-m.done
	m.capture(context.WithoutCancel(ctx))
	m.history.Available = len(m.history.Samples) > 1
	return m.history
}

func (m *resourceMonitor) setPhase(phase string) { m.phase.Store(&phase) }

func (m *resourceMonitor) capture(ctx context.Context) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// #nosec G204 -- reads only accounting fields for the owned server PID, never command arguments.
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(m.pid), "-o", "time=", "-o", "rss=")
	raw, err := cmd.Output()
	if err != nil {
		if parent.Err() == nil {
			m.history.Errors++
		}
		return
	}
	cpu, rss, err := parseResources(string(raw))
	if err != nil {
		m.history.Errors++
		return
	}
	now := time.Now()
	sample := resourceSample{Seconds: now.Sub(m.start).Seconds(), Phase: *m.phase.Load(), RSS: rss}
	if !m.previous.IsZero() && cpu >= m.previousCPU && now.After(m.previous) {
		percent := (cpu - m.previousCPU) * 100 / now.Sub(m.previous).Seconds()
		sample.CPU = &percent
	}
	m.previous, m.previousCPU = now, cpu
	m.history.Samples = append(m.history.Samples, sample)
}

func parseResources(line string) (cpuSeconds float64, rssBytes int64, err error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return 0, 0, errors.New("missing process accounting")
	}
	rss, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || rss < 0 || rss > math.MaxInt64/1024 {
		return 0, 0, errors.New("invalid resident memory")
	}
	value := fields[0]
	if days, rest, ok := strings.Cut(value, "-"); ok {
		n, err := strconv.ParseUint(days, 10, 32)
		if err != nil {
			return 0, 0, errors.New("invalid CPU days")
		}
		cpuSeconds = float64(n) * 24 * 60 * 60
		value = rest
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, errors.New("invalid CPU time")
	}
	for i, part := range parts {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, 0, errors.New("invalid CPU component")
		}
		cpuSeconds += n * math.Pow(60, float64(len(parts)-i-1))
	}
	if math.IsInf(cpuSeconds, 0) {
		return 0, 0, errors.New("CPU accounting overflow")
	}
	return cpuSeconds, rss * 1024, nil
}
