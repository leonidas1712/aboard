package apiserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// recoveringTransport retires a failed connection pool. A request deadline alone
// cancels an HTTP/2 stream; it can leave the broken connection available for every
// later request. We return the original failure without replaying any write.
type recoveringTransport struct {
	mu      sync.Mutex
	current *http.Transport
}

func newTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	tr := base.Clone()
	tr.HTTP2 = &http.HTTP2Config{
		SendPingTimeout:  15 * time.Second,
		PingTimeout:      10 * time.Second,
		WriteByteTimeout: 15 * time.Second,
	}
	return &recoveringTransport{current: tr}
}

func (t *recoveringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	tr := t.current
	t.mu.Unlock()
	res, err := tr.RoundTrip(req)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.mu.Lock()
		if t.current == tr {
			t.current = tr.Clone()
		}
		t.mu.Unlock()
		tr.CloseIdleConnections()
	}
	return res, err
}

func (t *recoveringTransport) CloseIdleConnections() {
	t.mu.Lock()
	tr := t.current
	t.mu.Unlock()
	tr.CloseIdleConnections()
}
