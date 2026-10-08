package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type logSummary struct {
	Warnings   int            `json:"warnings"`
	Errors     int            `json:"errors"`
	Kinds      map[string]int `json:"kinds"`
	ReadFailed bool           `json:"read_failed,omitempty"`
}

type processDiagnostic struct {
	Role     string     `json:"role"`
	Index    int        `json:"index"`
	Running  bool       `json:"running_before_cleanup"`
	ExitCode *int       `json:"exit_code,omitempty"`
	Logs     logSummary `json:"logs"`
}

type runProgress struct {
	Stage                         string
	SetupStart, MeasurementStart  time.Time
	Stream, Poll, Handover, Write []time.Duration
}

func diagnoseLogs(reader io.Reader) (logSummary, error) {
	result := logSummary{Kinds: map[string]int{}}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 128<<10)
	for scanner.Scan() {
		line := scanner.Text()
		var record struct {
			Level string `json:"level"`
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &record) == nil {
			switch record.Level {
			case "WARN":
				result.Warnings++
			case "ERROR":
				result.Errors++
			default:
				continue
			}
			classifyLogError(result.Kinds, record.Error)
			continue
		}
		// Standard HTTP accept errors and Go panics are not structured application logs.
		lower := strings.ToLower(line)
		if strings.Contains(lower, "http: accept error:") {
			classifyLogError(result.Kinds, lower)
		}
		if strings.HasPrefix(lower, "http: panic serving ") || strings.HasPrefix(lower, "panic:") || strings.HasPrefix(lower, "fatal error:") {
			result.Kinds["runtime_panic"]++
		}
	}
	return result, scanner.Err()
}

func classifyLogError(kinds map[string]int, value string) {
	value = strings.ToLower(value)
	if strings.HasPrefix(value, "panic serving ") {
		kinds["runtime_panic"]++
	}
	for _, entry := range []struct {
		kind    string
		needles []string
	}{
		{"open_files", []string{"too many open files"}},
		{"socket_reset", []string{"connection reset by peer"}},
		{"timeout", []string{"context deadline exceeded", "i/o timeout"}},
		{"sqlite_busy", []string{"database is locked", "database is busy", "sqlite_busy", "sqlite_locked"}},
	} {
		for _, needle := range entry.needles {
			if strings.Contains(value, needle) {
				kinds[entry.kind]++
				break
			}
		}
	}
}

func trackProcess(m *machine) {
	m.done = make(chan struct{})
	go func() { _ = m.cmd.Wait(); m.exitCode = m.cmd.ProcessState.ExitCode(); close(m.done) }()
}

func (f *fixture) processSummaries() []processDiagnostic {
	var results []processDiagnostic
	machines := append([]*machine{f.admin}, f.people...)
	for index, m := range machines {
		if m == nil || m.cmd == nil {
			continue
		}
		role, number := "daemon", index-1
		if index == 0 {
			role, number = "server", 0
		}
		result := processDiagnostic{Role: role, Index: number, Running: true, Logs: logSummary{Kinds: map[string]int{}}}
		select {
		case <-m.done:
			result.Running = false
			code := m.exitCode
			result.ExitCode = &code
		default:
		}
		if m.home != "" {
			file, err := os.Open(filepath.Join(m.home, "process.log"))
			if err != nil {
				result.Logs.ReadFailed = true
			} else {
				stat, err := file.Stat()
				if err != nil {
					result.Logs.ReadFailed = true
				} else {
					// Freeze the byte boundary so later log writes do not extend diagnostics.
					result.Logs, err = diagnoseLogs(io.NewSectionReader(file, 0, stat.Size()))
					result.Logs.ReadFailed = err != nil
				}
				_ = file.Close()
			}
		}
		results = append(results, result)
	}
	return results
}

func (f *fixture) finishReport(r *report, progress runProgress, runErr error) {
	r.Status = "complete"
	r.Stage = ""
	if runErr != nil {
		r.Status = "incomplete"
		r.Stage = progress.Stage
	}
	if !progress.SetupStart.IsZero() && r.Setup == 0 {
		r.Setup = time.Since(progress.SetupStart).Seconds()
	}
	if !progress.MeasurementStart.IsZero() {
		if r.Measurement == 0 {
			r.Measurement = time.Since(progress.MeasurementStart).Seconds()
		}
		if r.Measurement > 0 {
			r.Throughput = float64(r.Posts) / r.Measurement
		}
	}
	r.Throttles = int(f.throttles.Load())
	r.Daemons = 0
	for _, p := range f.people {
		if p.cmd != nil {
			r.Daemons++
		}
	}
	r.Stream = partialDistribution(progress.Stream)
	r.LongPoll = partialDistribution(progress.Poll)
	r.Handover = partialDistribution(progress.Handover)
	r.Write = partialDistribution(progress.Write)
	if runErr != nil {
		r.Processes = f.processSummaries()
	}
}

func partialDistribution(values []time.Duration) distribution {
	if len(values) == 0 {
		return distribution{}
	}
	result, err := summarize(values)
	if err != nil {
		return distribution{Samples: len(values)}
	}
	return result
}
