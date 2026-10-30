package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

func TestSayReturnsCommittedMessageWhenAdvisoryReadsStall(t *testing.T) {
	address, owner := testServer(t)
	cred := seatOn(t, address, owner)
	target, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	forward := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target) }}
	var posted atomic.Bool
	var posts atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/boards/"+cred.Board+"/messages" {
			posts.Add(1)
			posted.Store(true)
			forward.ServeHTTP(w, r)
			return
		}
		if posted.Load() && r.Method == http.MethodGet {
			<-r.Context().Done()
			return
		}
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(proxy.Close)
	cred.Server = proxy.URL
	a := inboxApp(t)
	var output bytes.Buffer
	a.env.Stdout, a.env.Stderr, a.json = &output, io.Discard, true
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	began := time.Now()
	err = runSay(ctx, a, []string{"--as", cred.Name, "--board", cred.Board, "--expect-reply", "stored despite slow notes"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(began) >= 3*time.Second {
		t.Fatal("successful send waited for advisory reads until the outer timeout")
	}
	var result sayOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 || result.Message.Seq == 0 || result.Message.Body != "stored despite slow notes" {
		t.Fatalf("lost successful send: posts=%d output=%s", posts.Load(), output.Bytes())
	}
}

func TestInboxReadHandshakeStopsWhenCallerCancels(t *testing.T) {
	a := inboxApp(t)
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req delivery.Request
		_ = delivery.ReadFrame(bufio.NewReader(conn), &req)
		accepted <- conn
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *inboxRead, 1)
	go func() {
		done <- a.startInboxRead(ctx, delivery.AgentRef{Server: "http://example.invalid", Board: "review", Name: "reviewer"}, false)
	}()
	conn := <-accepted
	defer func() { _ = conn.Close(); <-finished }()
	cancel()
	select {
	case rd := <-done:
		rd.done()
	case <-time.After(time.Second):
		// Stop the fixture without waiting for the production socket timeout.
		_ = listener.Close()
		t.Fatal("inbox handshake ignored caller cancellation")
	}
}
