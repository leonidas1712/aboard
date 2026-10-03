// Package codex is the delivery adapter for Codex. It hands bundles to a thread with
// `codex queue`, which Codex starts once the thread's current turn ends, and checks a
// thread through `codex app-server` (JSON-RPC over standard input and output) before an
// agent is bound to it, refusing sub-agent threads.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Adapter runs the codex command found on the PATH.
type Adapter struct {
	// ClientVersion is sent to the app server as the client's version.
	ClientVersion string
}

var _ delivery.Adapter = (*Adapter)(nil)

// Harness returns "codex".
func (*Adapter) Harness() string { return "codex" }

// WaitsForIdle is false: Codex queues a bundle until the thread's turn ends.
func (*Adapter) WaitsForIdle() bool { return false }

// Hand queues the bundle for the thread. The message is passed as an argument, never
// through a shell. Exit status 0 means Codex took it, which confirms it.
func (a *Adapter) Hand(ctx context.Context, h delivery.Handover) (bool, error) {
	if h.Bundle == "" {
		return false, errors.New("codex queue: empty bundle")
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		return false, fmt.Errorf("codex queue: %w", err)
	}
	var stderr bytes.Buffer
	// The bundle is one element of the argument vector; no shell sees it.
	cmd := &exec.Cmd{
		Path:   path,
		Args:   []string{"codex", "queue", "--thread", h.SessionID, "--message", h.Bundle},
		Stdout: io.Discard,
		Stderr: &limited{buf: &stderr, max: 4096},
	}
	if err := runWithContext(ctx, cmd); err != nil {
		return false, queueError(stderr.String(), err)
	}
	return true, nil
}

// runWithContext runs cmd and kills it if ctx ends first.
func runWithContext(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = cmd.Process.Kill() })
	defer stop()
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %w", ctx.Err(), err)
		}
		return fmt.Errorf("run %s: %w", cmd.Path, err)
	}
	return nil
}

// queueError classifies a failed codex queue run by what it printed.
func queueError(stderr string, err error) error {
	msg := firstLine(stderr)
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "no rollout found"), strings.Contains(low, "thread not found"),
		strings.Contains(low, "no active session found"):
		return fmt.Errorf("%w: %s", delivery.ErrTargetAbsent, msg)
	case strings.Contains(low, "sub-agent"), strings.Contains(low, "subagent"):
		return fmt.Errorf("%w: %s", delivery.ErrSubAgent, msg)
	}
	if msg == "" {
		return fmt.Errorf("codex queue: %w", err)
	}
	return fmt.Errorf("codex queue: %s: %w", msg, err)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// limited keeps the first max bytes written to it.
type limited struct {
	buf *bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Validate reads the exact thread through the app server and refuses a thread that
// doesn't exist or that a parent thread spawned, saying so in Codex's words.
func (a *Adapter) Validate(ctx context.Context, threadID string) error {
	err := a.validate(ctx, threadID)
	switch {
	case errors.Is(err, delivery.ErrSubAgent):
		return &delivery.SessionError{
			Err:     err,
			Message: "This Codex thread (" + threadID + ") is a sub-agent, and messages can only go to the root conversation.",
			Hint:    "Run aboard join or aboard resume in the root Codex conversation instead.",
		}
	case errors.Is(err, delivery.ErrTargetAbsent):
		return &delivery.SessionError{
			Err:     err,
			Message: "Codex has no thread " + threadID + ".",
			Hint:    "Run the command inside a Codex session, or open that thread again.",
		}
	}
	return err
}

func (a *Adapter) validate(ctx context.Context, threadID string) error {
	thread, err := a.readThread(ctx, threadID)
	if err != nil {
		return err
	}
	if thread.ParentThreadID != nil && *thread.ParentThreadID != "" {
		return fmt.Errorf("%w: parent thread %s", delivery.ErrSubAgent, *thread.ParentThreadID)
	}
	// A spawned thread read from disk can have no parentThreadId; its source says
	// {"subAgent": {...}}.
	var source map[string]json.RawMessage
	if json.Unmarshal(thread.Source, &source) == nil {
		if _, ok := source["subAgent"]; ok {
			return fmt.Errorf("%w: source %s", delivery.ErrSubAgent, thread.Source)
		}
	}
	if thread.ThreadSource != nil && *thread.ThreadSource == "subagent" {
		return fmt.Errorf("%w: thread source subagent", delivery.ErrSubAgent)
	}
	return nil
}

// thread is the part of the app server's Thread the adapter reads.
type thread struct {
	ID             string          `json:"id"`
	ParentThreadID *string         `json:"parentThreadId"`
	Source         json.RawMessage `json:"source"`
	ThreadSource   *string         `json:"threadSource"`
}

type rpcRequest struct {
	ID     *int   `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcMessage struct {
	ID     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Result json.RawMessage  `json:"result"`
	Error  *rpcError        `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// readThread starts `codex app-server`, initializes the connection and reads one thread.
// The app server writes one JSON message per line, with notifications in between and
// without a "jsonrpc" field.
func (a *Adapter) readThread(ctx context.Context, threadID string) (*thread, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limited{buf: &stderr, max: 4096}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		cancel()
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 64<<10), 16<<20)
	call := func(id int, method string, params any) (*rpcMessage, error) {
		if err := enc.Encode(rpcRequest{ID: &id, Method: method, Params: params}); err != nil {
			return nil, fmt.Errorf("codex app-server %s: %w", method, err)
		}
		want := fmt.Sprint(id)
		for lines.Scan() {
			var m rpcMessage
			if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil || m.Method != "" {
				continue // notifications and requests from the server
			}
			if strings.Trim(string(*m.ID), `"`) == want {
				return &m, nil
			}
		}
		if err := lines.Err(); err != nil {
			return nil, fmt.Errorf("codex app-server %s: %w", method, err)
		}
		return nil, fmt.Errorf("codex app-server ended before answering %s: %s", method, firstLine(stderr.String()))
	}

	version := a.ClientVersion
	if version == "" {
		version = "0"
	}
	init, err := call(1, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "aboard", "title": "Aboard", "version": version},
	})
	if err != nil {
		return nil, err
	}
	if init.Error != nil {
		return nil, fmt.Errorf("codex app-server initialize: %s (code %d)", init.Error.Message, init.Error.Code)
	}
	if err := enc.Encode(rpcRequest{Method: "initialized"}); err != nil {
		return nil, fmt.Errorf("codex app-server initialized: %w", err)
	}
	resp, err := call(2, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		msg := strings.ToLower(resp.Error.Message)
		if strings.Contains(msg, "thread not loaded") || strings.Contains(msg, "not found") || strings.Contains(msg, "invalid thread id") {
			return nil, fmt.Errorf("%w: %s", delivery.ErrTargetAbsent, resp.Error.Message)
		}
		return nil, fmt.Errorf("codex app-server thread/read: %s (code %d)", resp.Error.Message, resp.Error.Code)
	}
	var result struct {
		Thread *thread `json:"thread"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil || result.Thread == nil {
		return nil, fmt.Errorf("codex app-server thread/read: unexpected answer %.200s", resp.Result)
	}
	return result.Thread, nil
}

// Version returns what `codex --version` prints, such as "codex-cli 0.159.3".
func (a *Adapter) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "codex", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("codex --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// HasQueue reports whether this Codex has the queue command.
func (a *Adapter) HasQueue(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "codex", "queue", "--help")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}
