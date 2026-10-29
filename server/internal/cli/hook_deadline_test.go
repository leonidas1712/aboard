package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

func TestPromptReturnsBeforeHarnessDeadlineWhenDaemonStalls(t *testing.T) {
	a := inboxApp(t)
	var out bytes.Buffer
	a.env.Stdout, a.env.Stderr = &out, io.Discard
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	l, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer func() { _ = c.Close() }(); _, _ = io.Copy(io.Discard, c) }(c)
		}
	}()
	a.daemonChecked = true
	h := hookCall{a: a, harness: "claude-code"}
	h.in.SessionID = "stalled-prompt"
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	start := time.Now()
	_ = h.turnStart(ctx, false)
	if elapsed := time.Since(start); elapsed >= 7*time.Second {
		t.Fatalf("prompt blocked for %v; must return before harness cutoff", elapsed)
	}
	if out.Len() != 0 {
		t.Fatalf("stalled daemon produced context: %q", out.String())
	}
}

type lockedHookOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedHookOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedHookOutput) has(s string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.Buffer.Bytes(), []byte(s))
}

func TestPromptWritesBundleBeforeReceipt(t *testing.T) {
	for _, failOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "printed", true: "failed-output"}[failOutput], func(t *testing.T) {
			a := inboxApp(t)
			var out lockedHookOutput
			a.env.Stdout, a.env.Stderr = &out, io.Discard
			if failOutput {
				a.env.Stdout = failedHookOutput{}
			}
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			l, err := control.Listen(p.socket())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = l.Close() })
			received := make(chan bool, 1)
			go func() {
				for {
					c, err := l.Accept()
					if err != nil {
						return
					}
					var req delivery.Request
					if delivery.ReadFrame(bufio.NewReader(c), &req) != nil {
						_ = c.Close()
						continue
					}
					res := delivery.Response{V: delivery.ProtocolVersion}
					switch req.Op {
					case "turn_start_receipted":
						res.Bundle = "queued message"
						res.HandoffID = "hnd_prompt"
						res.Boot = "boot"
					case delivery.OpReceived:
						received <- out.has("queued message")
					}
					_ = delivery.WriteFrame(c, res)
					_ = c.Close()
				}
			}()
			a.daemonChecked = true
			h := hookCall{a: a, harness: "claude-code"}
			h.in.SessionID = "prompt-receipt"
			err = h.turnStart(t.Context(), true)
			if failOutput {
				if err == nil {
					t.Fatal("output failure was ignored")
				}
				select {
				case <-received:
					t.Fatal("failed output acknowledged message")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case shown := <-received:
				if !shown {
					t.Fatal("receipt preceded output")
				}
			case <-time.After(time.Second):
				t.Fatal("no receipt after prompt output")
			}
		})
	}
}
