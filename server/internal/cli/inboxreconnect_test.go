package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestAWaitingInboxSurvivesAConnectionReset(t *testing.T) {
	address, owner := testServer(t)
	cred := seatOn(t, address, owner)
	postTo(t, address, owner, cred.Board, 1)
	target, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	forward := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target) }}
	var waits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me/inbox" && r.URL.Query().Get("wait") != "" && waits.Add(1) == 1 {
			closeInboxConnection(t, w)
			return
		}
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	cred.Server = proxy.URL
	a := inboxApp(t)
	a.env.Stdout, a.env.Stderr = io.Discard, io.Discard
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	if err := runInbox(context.Background(), a, []string{"--as", cred.Name, "--board", cred.Board, "--wait", "3", "--peek"}); err != nil {
		t.Fatalf("waiting inbox did not recover: %v", err)
	}
	if waits.Load() < 2 {
		t.Fatal("the waiting read was not retried")
	}
}

func TestAWaitingInboxNeverRetriesAnAuthorityRefusal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-seat" {
			t.Error("the read did not retain its seat credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"agent_removed","message":"removed","hint":"ask your person"}}`)
	}))
	t.Cleanup(server.Close)
	a := inboxApp(t)
	c, err := a.newClient(serverRef{URL: server.URL}, "test-seat", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.waitInbox(context.Background(), time.Now().Add(time.Second), api.GetInboxParams{})
	if err != nil || res.StatusCode() != http.StatusForbidden || calls.Load() != 1 {
		t.Fatalf("refusal was retried: calls %d, error %v", calls.Load(), err)
	}
}

func TestAWaitingInboxStopsRetryingAtItsOriginalDeadline(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		closeInboxConnection(t, w)
	}))
	t.Cleanup(server.Close)
	a := inboxApp(t)
	c, err := a.newClient(serverRef{URL: server.URL}, "test-seat", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.waitInbox(context.Background(), time.Now().Add(20*time.Millisecond), api.GetInboxParams{})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("retry extended the original wait: calls %d, error %v", calls.Load(), err)
	}
}

func TestCancellingAWaitingInboxCancelsItsBackoff(t *testing.T) {
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		closeInboxConnection(t, w)
		close(entered)
	}))
	t.Cleanup(server.Close)
	a := inboxApp(t)
	c, err := a.newClient(serverRef{URL: server.URL}, "test-seat", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := c.waitInbox(ctx, time.Now().Add(time.Minute), api.GetInboxParams{})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("read never reached the server: %v", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled wait succeeded")
	}
}

func closeInboxConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("test server cannot hijack the connection")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = conn.Close()
}
