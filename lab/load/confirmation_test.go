package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadConfirmsEveryExtensionBeforeWaitingForServerAcks(t *testing.T) {
	testLoadConfirmation(t, false, false)
}

func TestLoadRedialsIdleObserversBeforePosting(t *testing.T) {
	testLoadConfirmation(t, true, false)
}

func TestLoadVerifiesAcknowledgmentsConcurrently(t *testing.T) {
	testLoadConfirmation(t, false, true)
}

func testLoadConfirmation(t *testing.T, closeIdle, parallelChecks bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var confirmed atomic.Int64
	var checking atomic.Int64
	var release sync.Once
	ready := make(chan struct{})
	var workers sync.WaitGroup
	defer workers.Wait()
	p := &machine{heads: &headLog{changed: make(chan struct{})}}
	var connections []net.Conn
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	for i := range 2 {
		name := fmt.Sprintf("board-%d", i)
		local, remote := net.Pipe()
		connections = append(connections, local, remote)
		s := &seat{Board: name, MemberID: name, Token: name, board: &board{ID: name, Name: name, Head: 1}, ext: &extension{conn: local, frames: make(chan frame, 1), err: make(chan error, 1)}}
		p.seats = append(p.seats, s)
		workers.Go(func() {
			decoder := json.NewDecoder(remote)
			for range 3 {
				var response map[string]any
				if err := decoder.Decode(&response); err != nil {
					return
				}
				if response["op"] == "received" {
					confirmed.Add(1)
				}
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		name := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/v1/me" {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": name})
			return
		}
		if r.Method == http.MethodPost {
			for i, s := range p.seats {
				if !strings.Contains(r.URL.Path, s.Board) {
					continue
				}
				at := time.Now()
				s.ext.frames <- frame{Event: "deliver", ID: int64(i + 1), Bundle: fmt.Sprintf(`<aboard-message board=%q seq="2">load-r0-b%d</aboard-message>`, s.Board, i), at: at}
				p.heads.mu.Lock()
				p.heads.heads = append(p.heads.heads, observation{Key: messageKey{s.board.ID, 2}, At: at})
				p.heads.mu.Unlock()
			}
			_ = json.NewEncoder(w).Encode(map[string]int{"seq": 2})
			return
		}
		if r.URL.Query().Get("wait") == "" {
			if parallelChecks {
				if checking.Add(1) == 2 {
					release.Do(func() { close(ready) })
				}
				select {
				case <-ready:
				case <-ctx.Done():
					return
				}
			}
			if confirmed.Load() != 2 {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "confirmation_barrier_not_reached"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"cursor": 2, "messages": []any{}})
			return
		}
		for i, s := range p.seats {
			if s.Token == name {
				_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{map[string]any{"seq": 2, "body": fmt.Sprintf("load-r0-b%d", i)}}})
				return
			}
		}
	}))
	defer server.Close()
	f := &fixture{ctx: ctx, client: server.Client(), url: server.URL, admin: &machine{}, people: []*machine{p}, boards: []*board{p.seats[0].board, p.seats[1].board}}
	defer func() {
		cancel()
		f.workers.Wait()
		for _, s := range p.seats {
			s.observer.CloseIdleConnections()
		}
	}()
	if err := f.primeObservers(); err != nil {
		t.Fatal(err)
	}
	if closeIdle {
		for _, seat := range p.seats {
			seat.observer.CloseIdleConnections()
		}
	}
	checks := deliveryCheck{Expected: map[string][]messageKey{}, Seen: map[string][]messageKey{}}
	var streams, polls, handovers, writes []time.Duration
	var report report
	if err := f.round(ctx, 0, &checks, &streams, &polls, &handovers, &writes, &report); err != nil {
		t.Fatal(err)
	}
	if confirmed.Load() != 2 || report.Deliveries != 2 {
		t.Fatalf("missing confirmations: %d, deliveries: %d", confirmed.Load(), report.Deliveries)
	}
	if err := checks.validate(); err != nil {
		t.Fatal(err)
	}
}
