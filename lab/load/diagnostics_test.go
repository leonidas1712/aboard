package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLoadDiagnosticsCountFailuresWithoutLogContent(t *testing.T) {
	const sensitive = "fixture-secret-do-not-render"
	logs := strings.Join([]string{
		`{"level":"INFO","msg":"` + sensitive + `","token":"` + sensitive + `"}`,
		`{"level":"WARN","msg":"request failed","error":"accept: too many open files; ` + sensitive + `"}`,
		`{"level":"ERROR","msg":"request failed","error":"database is locked; body=` + sensitive + `"}`,
		`http: panic serving 127.0.0.1:1: ` + sensitive,
		`{"level":"ERROR","msg":"request failed","error":"panic serving GET /v1/me/inbox: ` + sensitive + `"}`,
		`{"level":"ERROR","msg":"request failed","error":"read: connection reset by peer; ` + sensitive + `"}`,
		`{"level":"WARN","msg":"request failed","error":"context deadline exceeded; ` + sensitive + `"}`,
		sensitive,
	}, "\n")
	summary, err := diagnoseLogs(strings.NewReader(logs))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Warnings != 2 || summary.Errors != 3 {
		t.Fatalf("wrong severity counts: %+v", summary)
	}
	for _, kind := range []string{"open_files", "sqlite_busy", "runtime_panic", "socket_reset", "timeout"} {
		want := 1
		if kind == "runtime_panic" {
			want = 2
		}
		if summary.Kinds[kind] != want {
			t.Fatalf("category %s: %+v", kind, summary.Kinds)
		}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sensitive) || strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "request failed") {
		t.Fatalf("diagnostics copied log content: %s", raw)
	}
}

func TestLoadDiagnosticsDoNotInferAnUnknownFailure(t *testing.T) {
	summary, err := diagnoseLogs(strings.NewReader(`{"level":"ERROR","msg":"unexpected custom failure","body":"private timeout: database is locked"}`))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Errors != 1 || len(summary.Kinds) != 0 {
		t.Fatalf("unknown failure received an invented category: %+v", summary)
	}
}

func TestLoadFailureRetainsCompletedWorkAndPartialTimings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			<-ctx.Done()
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_, _ = io.WriteString(w, `{"seq":3}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"injected_failure"}}`)
	}))
	defer server.Close()
	b1, b2 := &board{ID: "first", Name: "first", Head: 2}, &board{ID: "second", Name: "second", Head: 2}
	p := &machine{seats: []*seat{{board: b1}, {board: b2}}}
	f := &fixture{ctx: ctx, url: server.URL, client: server.Client(), admin: &machine{}, people: []*machine{p}, boards: []*board{b1, b2}}
	defer func() { cancel(); f.workers.Wait() }()
	f.throttles.Store(2)
	r := report{Daemons: 1}
	progress := runProgress{Stage: "round", SetupStart: time.Now().Add(-time.Second), MeasurementStart: time.Now(), Stream: []time.Duration{time.Millisecond}, Poll: []time.Duration{2 * time.Millisecond}, Handover: []time.Duration{3 * time.Millisecond}}
	checks := deliveryCheck{Expected: map[string][]messageKey{}, Seen: map[string][]messageKey{}}
	err := f.round(ctx, 0, &checks, &progress.Stream, &progress.Poll, &progress.Handover, &progress.Write, &r)
	if err == nil {
		t.Fatal("forced round failure succeeded")
	}
	f.finishReport(&r, progress, err)
	if r.Status != "incomplete" || r.Stage != "round" || r.Posts != 1 || r.Throttles != 2 || r.Setup <= 0 || r.Measurement <= 0 {
		t.Fatalf("failure lost completed work: %+v", r)
	}
	if r.Stream.Samples != 1 || r.LongPoll.Samples != 1 || r.Handover.Samples != 1 {
		t.Fatalf("failure lost collected samples: %+v", r)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var measured struct {
		Write distribution `json:"request_to_write_response"`
	}
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	if measured.Write.Samples != 1 || measured.Write.P50 <= 0 {
		t.Fatalf("failure lost the successful write response timing: %+v", measured.Write)
	}
}

func TestLoadRecordsChildExitBeforeCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// A closed stdin makes cat exit naturally; an open pipe keeps its sibling alive.
	exited := &machine{}
	exited.cmd = exec.CommandContext(ctx, "cat")
	if err := exited.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	trackProcess(exited)
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-exited.done:
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	live := &machine{}
	live.cmd = exec.CommandContext(ctx, "cat")
	live.cmd.Stdin = reader
	if err = live.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	trackProcess(live)
	f := &fixture{client: &http.Client{}, admin: exited, people: []*machine{live}}
	before := f.processSummaries()
	f.close()
	if len(before) != 2 || before[0].Role != "server" || before[0].Running || before[0].ExitCode == nil || *before[0].ExitCode != 0 {
		t.Fatalf("natural exit was lost: %+v", before)
	}
	if !before[1].Running || before[1].ExitCode != nil {
		t.Fatalf("cleanup kill attributed to original failure: %+v", before[1])
	}
}

func TestLoadCompleteReportHasNoFailedStage(t *testing.T) {
	f := &fixture{}
	r := report{Status: "incomplete", Stage: "topology"}
	f.finishReport(&r, runProgress{Stage: "audit"}, nil)
	if r.Status != "complete" || r.Stage != "" {
		t.Fatalf("successful run retained a failed stage: %+v", r)
	}
}
