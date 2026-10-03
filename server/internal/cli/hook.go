package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// hookExit is the exit code a hook command ends with, without printing an error.
type hookExit int

func (e hookExit) Error() string { return fmt.Sprintf("hook exit %d", int(e)) }

// exitWake is the exit code that makes a harness wake the session with a waiting
// hook's standard error.
const exitWake = 2

// hookInputLimit is the most hook input read from standard input.
const hookInputLimit = 1 << 20

// sessionIDPattern is what a session id may contain. It is written into a file the
// harness sources as shell, so nothing else is accepted.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// runHook runs "aboard hook <harness> <event>": the commands harness hooks call. A hook
// that fails for any reason other than a delivery exits 0, so a broken daemon never
// blocks a session.
func runHook(ctx context.Context, a *app, args []string) error {
	use := usageOf("hook")
	fs := a.flags("hook")
	pos, err := a.parse(fs, args, use, 2, 2)
	if err != nil {
		return err
	}
	name, event := pos[0], pos[1]
	started := time.Now()
	var in harness.HookInput
	data, err := io.ReadAll(io.LimitReader(a.env.Stdin, hookInputLimit))
	if err == nil {
		err = json.Unmarshal(data, &in)
	}
	if err != nil || !sessionIDPattern.MatchString(in.SessionID) {
		a.hookNote("the hook input has no usable session_id")
		return hookExit(0)
	}
	hs, known := a.registry().Get(name)
	var call harness.Call
	if known {
		call, known = hs.HookCall(event, in)
	}
	if !known {
		return usageError(fmt.Sprintf("%q is not a hook event for %q.", event, name), use)
	}
	h := hookCall{a: a, harness: name, in: in, boot: a.env.Getenv("ABOARD_BOOT"), started: started}
	var hookErr error
	switch call.Op {
	case harness.OpSessionStart:
		if envFile := hs.Profile().Identity.EnvFile; envFile != "" {
			hookErr = h.sessionStartWithEnvFile(ctx, envFile)
			break
		}
		var resp delivery.Response
		if resp, hookErr = h.call(ctx, delivery.OpRegister); hookErr == nil {
			h.startNote(resp)
		}
	case harness.OpPrompt:
		req := h.request(delivery.OpPrompt)
		// A prompt that hands back a waiting hook's wake text is the wake itself, so it
		// must not count as the session's next event.
		req.Wake = call.Wake
		_, hookErr = h.a.callDaemon(ctx, req)
	case harness.OpWait:
		return h.stop(ctx)
	case harness.OpTurnEnd:
		_, hookErr = h.call(ctx, delivery.OpTurnEnd)
	case harness.OpEnd:
		_, hookErr = h.call(ctx, delivery.OpEnd)
	case harness.OpTool:
		hookErr = h.tool(ctx)
	default:
		return usageError(fmt.Sprintf("%q is not a hook event for %q.", event, name), use)
	}
	if hookErr != nil {
		a.hookNote(asError(hookErr).Message)
	}
	return hookExit(0)
}

// hookNote prints a one-line note about a hook that did nothing.
func (a *app) hookNote(msg string) {
	_, _ = fmt.Fprintln(a.env.Stderr, "aboard hook: "+msg)
}

type hookCall struct {
	a       *app
	harness string
	in      harness.HookInput
	boot    string
	started time.Time
}

func (h hookCall) request(op string) delivery.Request {
	return delivery.Request{Op: op, Harness: h.harness, Session: h.in.SessionID, Boot: h.boot, Source: h.in.Source}
}

func (h hookCall) call(ctx context.Context, op string) (delivery.Response, error) {
	return h.a.callDaemon(ctx, h.request(op))
}

// sessionStartWithEnvFile registers the session and writes ABOARD_SESSION and
// ABOARD_BOOT to the session's environment file, which the variable envVar names, so
// every command the agent runs carries them. A compacted session keeps its boot id;
// anything else is a new process with a new one.
func (h hookCall) sessionStartWithEnvFile(ctx context.Context, envVar string) error {
	h.boot = ""
	if h.in.Source != "compact" {
		boot, err := newBootID(h.a.env.Rand)
		if err != nil {
			return err
		}
		h.boot = boot
	}
	resp, err := h.call(ctx, delivery.OpRegister)
	if err != nil {
		return err
	}
	h.startNote(resp)
	envFile := h.a.env.Getenv(envVar)
	if envFile == "" {
		return nil
	}
	lines := "export ABOARD_SESSION=" + h.harness + ":" + h.in.SessionID + "\n"
	if sessionIDPattern.MatchString(resp.Boot) {
		lines += "export ABOARD_BOOT=" + resp.Boot + "\n"
	}
	f, err := os.OpenFile(filepath.Clean(envFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", envVar, err)
	}
	if _, err := f.WriteString(lines); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", envVar, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", envVar, err)
	}
	return nil
}

// startNote tells a session that starts again with the same session id (the harness
// resumed it) which agent it is, or that another session resumed its agent meanwhile.
// The harnesses add a session-start hook's output to the session's context. A new
// session gets nothing.
func (h hookCall) startNote(resp delivery.Response) {
	var note string
	switch {
	case resp.Reopened && len(resp.Agents) == 1:
		a := resp.Agents[0]
		note = fmt.Sprintf("Aboard: this session is %s on %s again, as it was before it closed; messages that waited for %s arrive when this turn ends.", a.Name, a.Board, a.Name)
	case resp.Lost != nil:
		a := resp.Lost
		note = fmt.Sprintf("Aboard: this session was %s on %s until another session resumed %s; it has no agent now. "+
			"To act as %s here again, run aboard resume %s, which leaves the other session without it.", a.Name, a.Board, a.Name, a.Name, a.Name)
	default:
		return
	}
	_, _ = fmt.Fprintln(h.a.env.Stdout, note)
}

func newBootID(rnd io.Reader) (string, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	b := make([]byte, 8)
	if _, err := io.ReadFull(rnd, b); err != nil {
		return "", fmt.Errorf("read randomness for a boot id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// tool adds to a busy turn, at a tool boundary, the owner's messages and a notice of the
// other messages waiting. It never blocks or changes the tool call.
func (h hookCall) tool(ctx context.Context) error {
	if h.in.AgentID != "" {
		return nil // a sub-agent's tool call; messages go to the root conversation
	}
	req := h.request(delivery.OpBoundary)
	req.Started = h.started
	resp, err := h.a.callDaemon(ctx, req)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(resp.Bundle + "\n\n" + resp.Notice)
	if text == "" {
		return nil
	}
	event := h.in.HookEventName
	if event == "" {
		event = "PostToolUse"
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.AdditionalContext = text
	enc := json.NewEncoder(h.a.env.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("print hook output: %w", err)
	}
	return nil
}

// stopRetries is how many times in a row the stop hook tries to reach the daemon
// before giving up and letting the session go on without delivery.
const stopRetries = 5

// stop waits while the session is idle. On a delivery it writes the bundle to standard
// error and exits 2, which wakes the session with it; when released it exits 0. If the
// daemon goes away, the hook starts it again and keeps waiting.
func (h hookCall) stop(ctx context.Context) error {
	resumed := false
	failures := 0
	for {
		conn, err := h.a.dialDaemon(ctx)
		if err != nil {
			failures++
			if failures >= stopRetries || ctx.Err() != nil {
				h.a.hookNote(asError(err).Message)
				return hookExit(0)
			}
			select {
			case <-ctx.Done():
				return hookExit(0)
			case <-time.After(time.Duration(failures) * 200 * time.Millisecond):
			}
			continue
		}
		req := withHarnessProcess(h.request(delivery.OpWait))
		req.V, req.Resumed, req.Started = delivery.ProtocolVersion, resumed, h.started
		code, done := h.waitOn(conn, req)
		_ = conn.Close()
		if done {
			return hookExit(code)
		}
		failures = 0
		resumed = true
	}
}

// waitOn sends a wait request and reads until a delivery or a release. done is false
// when the connection closed first.
func (h hookCall) waitOn(conn io.ReadWriter, req delivery.Request) (code int, done bool) {
	if err := delivery.WriteFrame(conn, req); err != nil {
		return 0, false
	}
	r := bufio.NewReader(conn)
	for {
		var resp delivery.Response
		if err := delivery.ReadFrame(r, &resp); err != nil {
			if errors.Is(err, delivery.ErrFrameTooLarge) {
				return 0, true
			}
			return 0, false
		}
		switch {
		case resp.Error != nil:
			h.a.hookNote(resp.Error.Message)
			return 0, true
		case resp.Event == delivery.EventDeliver:
			_ = delivery.WriteFrame(conn, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpReceived})
			_, _ = io.WriteString(h.a.env.Stderr, resp.Bundle+"\n")
			return exitWake, true
		case resp.Event == delivery.EventRelease:
			return 0, true
		}
	}
}
