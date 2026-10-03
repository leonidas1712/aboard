//go:build e2e

package e2e

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// extClient plays a harness extension, as omp's runs inside omp: it holds its session's
// connection to the delivery daemon, speaking only what spec/control.md says ("The
// extension connection"), so the kit drives the real daemon the way an extension does.
type extClient struct {
	t      *testing.T
	e      *env
	conn   net.Conn
	frames chan extFrame
	// hello is what the client sent to open the connection.
	hello map[string]any
}

// extFrame is one message from the daemon on an extension connection.
type extFrame struct {
	Event    string `json:"event"`
	ID       int64  `json:"id"`
	Bundle   string `json:"bundle"`
	Notice   string `json:"notice"`
	Boot     string `json:"boot"`
	Reopened bool   `json:"reopened"`
	Agents   []struct {
		Board string `json:"board"`
		Name  string `json:"name"`
	} `json:"agents"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// socketPath is where the env's delivery daemon listens: spec/control.md, Transport.
func (e *env) socketPath() string {
	state := e.stateDir()
	p := filepath.Join(state, "daemon.sock")
	if len(p) <= 100 {
		return p
	}
	sum := sha256.Sum256([]byte(state))
	return filepath.Join("/tmp", "aboard-"+strconv.Itoa(os.Getuid()), hex.EncodeToString(sum[:8])+".sock")
}

// extConnect connects an extension for the harness's session, starting the daemon as an
// extension does when its socket isn't there, and returns the client with the daemon's
// first answer.
func (e *env) extConnect(harness, id, boot, source string) (*extClient, extFrame) {
	e.t.Helper()
	conn, err := net.Dial("unix", e.socketPath())
	if err != nil {
		e.run("daemon", "start")
		if conn, err = net.Dial("unix", e.socketPath()); err != nil {
			e.t.Fatalf("connect to the delivery daemon at %s: %v", e.socketPath(), err)
		}
	}
	e.t.Cleanup(func() { _ = conn.Close() })
	c := &extClient{t: e.t, e: e, conn: conn, frames: make(chan extFrame, 16)}
	go func() {
		defer close(c.frames)
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			var f extFrame
			if json.Unmarshal([]byte(line), &f) == nil {
				c.frames <- f
			}
		}
	}()
	c.hello = map[string]any{
		"v": 1, "op": "hello", "harness": harness, "session": id, "boot": boot, "source": source,
		"process": map[string]any{"pid": os.Getpid()}, "cwd": e.dir, "extension_version": "kit",
	}
	c.send(c.hello)
	return c, c.next(10 * time.Second)
}

func (c *extClient) send(msg map[string]any) {
	c.t.Helper()
	msg["v"] = 1
	raw, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		c.t.Fatalf("send %v: %v", msg["op"], err)
	}
}

// next returns the next message the daemon sends.
func (c *extClient) next(within time.Duration) extFrame {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			c.t.Fatal("the daemon closed the extension's connection")
		}
		return f
	case <-time.After(within):
		c.t.Fatalf("the extension got nothing from the daemon within %s", within)
	}
	return extFrame{}
}

// deliver waits for the next bundle and, with confirm, answers received, as the extension
// does once it has added the bundle to its session.
func (c *extClient) deliver(within time.Duration, confirm bool) extFrame {
	c.t.Helper()
	f := c.next(within)
	if f.Event != "deliver" || f.ID == 0 || !strings.Contains(f.Bundle, "<aboard-messages") {
		c.t.Fatalf("want a bundle with a delivery id, got %+v", f)
	}
	if confirm {
		c.send(map[string]any{"op": "received", "id": f.ID})
	}
	return f
}

// boundary asks, on a connection of its own, what a tool boundary adds to the busy turn,
// as the extension does after each step that ran tools.
func (c *extClient) boundary() string {
	c.t.Helper()
	conn, err := net.Dial("unix", c.e.socketPath())
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	req := map[string]any{
		"v": 1, "op": "boundary", "harness": c.hello["harness"], "session": c.hello["session"], "boot": c.hello["boot"],
		"started": time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, _ := json.Marshal(req)
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		c.t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		c.t.Fatalf("read the boundary's answer: %v", err)
	}
	var f extFrame
	if err := json.Unmarshal([]byte(line), &f); err != nil || f.Error != nil {
		c.t.Fatalf("boundary: %s", line)
	}
	return strings.TrimSpace(f.Bundle + "\n\n" + f.Notice)
}

// closed waits until the daemon has closed the connection.
func (c *extClient) closed() {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if ok {
			c.t.Fatalf("want the connection closed, got %+v", f)
		}
	case <-time.After(10 * time.Second):
		c.t.Fatal("the daemon kept the extension's connection open")
	}
}
