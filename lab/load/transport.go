package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
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

func (f *fixture) primeObservers() error {
	for _, p := range f.people {
		for _, s := range p.seats {
			s.observer = &http.Client{Timeout: 65 * time.Second, Transport: loadTransport((&net.Dialer{}).DialContext, 16)}
			data, err := f.apiWithClient(f.ctx, s.observer, http.MethodGet, "/v1/me", s.Token, nil)
			if err != nil {
				return fmt.Errorf("prime observer: %w", err)
			}
			if str(data, "id") != s.MemberID {
				return errors.New("observer identity does not match its seat")
			}
		}
	}
	return nil
}
