package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type throttleTransport struct{ cancel context.CancelFunc }

func (t throttleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"1"}}, Body: closeThrottle{Reader: strings.NewReader(`{"error":{"code":"rate_limited"}}`), cancel: t.cancel}}, nil
}

type closeThrottle struct {
	io.Reader
	cancel context.CancelFunc
}

func (b closeThrottle) Close() error { b.cancel(); return nil }

func TestLoadThrottleCountsKeepKindsAndSetupWithoutRequestData(t *testing.T) {
	f := &fixture{url: "http://test.invalid"}
	for i, path := range []string{"/v1/join", "/v1/connect", "/v1/boards/private-name/messages"} {
		ctx, cancel := context.WithCancel(t.Context())
		client := &http.Client{Transport: throttleTransport{cancel: cancel}}
		_, err := f.apiWithClient(ctx, client, http.MethodPost, path, "fixture-secret-token", nil)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled retry: %v", err)
		}
		if i == 0 {
			f.setupThrottles.Store(f.throttles.Load())
		}
	}
	var r report
	f.finishReport(&r, runProgress{Stage: "round"}, nil)
	if r.Throttles != 3 || r.SetupThrottles != 1 || r.ThrottleKinds != (throttleKinds{Join: 1, Connect: 1, Other: 1}) {
		t.Fatalf("lost throttle counts: %+v", r)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-name", "fixture-secret-token", "test.invalid"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("report contains request data")
		}
	}
	f.finishReport(&r, runProgress{Stage: "setup"}, context.Canceled)
	if r.SetupThrottles != 3 || r.Status != "incomplete" {
		t.Fatalf("partial setup lost throttles: %+v", r)
	}
}
