package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLoadQueuesDialsWithoutLimitingOpenRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const requests = 64
	entered := make(chan struct{}, requests)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	transport := loadTransport((&net.Dialer{}).DialContext, 4)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	for range requests {
		workers.Go(func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
			if err != nil {
				t.Error(err)
				return
			}
			response, err := client.Do(req)
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		})
	}
	for range requests {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("dial limit also limited requests: all observers must enter before posting")
		}
	}
	close(release)
}

func TestLoadCancelsAQueuedDialWithoutStartingIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	transport := loadTransport(func(ctx context.Context, _, _ string) (net.Conn, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return nil, context.Canceled
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 1)
	first := make(chan error, 1)
	go func() { _, err := transport.DialContext(ctx, "tcp", "unused"); first <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	queued, stop := context.WithCancel(ctx)
	stop()
	if _, err := transport.DialContext(queued, "tcp", "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued dial: %v", err)
	}
	select {
	case <-entered:
		t.Fatal("queued cancelled request started another dial")
	default:
	}
	close(release)
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
