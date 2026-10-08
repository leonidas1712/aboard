package main

import (
	"context"
	"net"
	"net/http"
)

// loadTransport bounds connection establishment, not open requests: every observer
// must be able to wait concurrently before any measured message is posted.
func loadTransport(dial func(context.Context, string, string) (net.Conn, error), parallel int) *http.Transport {
	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	slots := make(chan struct{}, parallel)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return dial(ctx, network, addr)
	}
	return transport
}
