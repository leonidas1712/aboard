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
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
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
	if a.env.Getenv(headlessEnv) != "" {
		// A headless turn: its runner hands it the messages and reads none back.
		return hookExit(0)
	}
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
	if in.AgentID != "" && call.Op != harness.OpMarkSubagent {
		// A hook fired inside a subagent. Codex gives it the root's session_id, so it would
		// count as the root's prompt, tool boundary or turn end; the messages and the
		// session's state belong to the root conversation.
		return hookExit(0)
	}
	h := hookCall{a: a, harness: name, in: in, boot: a.env.Getenv("ABOARD_BOOT"), started: started}
	h.peerBoundary = hs.Profile().HasCapability("midturn-peer") && (name != "codex" || event == "post-tool")
	var hookErr error
	switch call.Op {
	case harness.OpSessionStart:
		if envFile := hs.Profile().Identity.EnvFile; envFile != "" {
			hookErr = h.sessionStartWithEnvFile(ctx, envFile, a.inheritedSessionVars(hs))
			break
		}
		var resp delivery.Response
		if resp, hookErr = h.call(ctx, delivery.OpRegister); hookErr == nil {
			h.startNote(resp)
			h.printAdded(ctx)
		}
	case harness.OpPrompt:
		hookErr = h.turnStart(ctx, call.Wake)
	case harness.OpWait:
		return h.stop(ctx)
	case harness.OpTurnEnd:
		_, hookErr = h.call(ctx, delivery.OpTurnEnd)
	case harness.OpEnd:
		_, hookErr = h.call(ctx, delivery.OpEnd)
	case harness.OpTool:
		hookErr = h.tool(ctx)
	case harness.OpMarkSubagent:
		hookErr = h.markSubagent()
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
	a            *app
	harness      string
	in           harness.HookInput
	boot         string
	started      time.Time
	completion   *delivery.Request
	continued    *bool
	peerBoundary bool
}

func (h hookCall) request(op string) delivery.Request {
	req := delivery.Request{Op: op, Harness: h.harness, Session: h.in.SessionID, Boot: h.boot, Source: h.in.Source, Cwd: h.in.Cwd}
	if op == delivery.OpRegister {
		// A session aboard swarm up started carries its launch ticket, which binds it to
		// its agent as it starts.
		req.Launch = h.a.launchTicket()
	}
	return req
}

func (h hookCall) call(ctx context.Context, op string) (delivery.Response, error) {
	return h.a.callDaemon(ctx, h.request(op))
}

// inheritedSessionVars lists the session variables of other harnesses that hs gives
// way to and that are set in this hook's environment. The harness was started from a
// command of that other harness's session, so its own commands must not carry them.
// Only variables another harness sets as its session id are listed: a marker such as
// OMPCODE may come from a harness that runs these hooks itself.
func (a *app) inheritedSessionVars(hs harness.Harness) []string {
	var out []string
	for _, v := range hs.Profile().Identity.YieldsTo {
		if a.env.Getenv(v) == "" {
			continue
		}
		for _, other := range a.registry() {
			if id := other.Profile().Identity; other != hs && id.Kind == "env" && id.Env == v {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// sessionStartWithEnvFile registers the session and writes ABOARD_SESSION and
// ABOARD_BOOT to the session's environment file, which the variable envVar names, so
// every command the agent runs carries them, and unsets the inherited session variables
// of an outer harness. A compacted session keeps its boot id; anything else is a new
// process with a new one.
func (h hookCall) sessionStartWithEnvFile(ctx context.Context, envVar string, inherited []string) error {
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
	h.printAdded(ctx)
	envFile := h.a.env.Getenv(envVar)
	if envFile == "" {
		return nil
	}
	lines := "export ABOARD_SESSION=" + h.harness + ":" + h.in.SessionID + "\n"
	if sessionIDPattern.MatchString(resp.Boot) {
		lines += "export ABOARD_BOOT=" + resp.Boot + "\n"
	}
	for _, v := range inherited {
		if envName.MatchString(v) {
			lines += "unset " + v + "\n"
		}
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
// resumed it) which agent it is, with its delivery mode, or that another session
// resumed its agent meanwhile: the daemon's note. The harnesses add a session-start
// hook's output to the session's context. A new session gets nothing. A daemon from an
// earlier build sends no note, and the hook writes it without the mode.
func (h hookCall) startNote(resp delivery.Response) {
	note := resp.Note
	switch {
	case note != "":
	case resp.Reopened && len(resp.Agents) == 1:
		note = deliverytext.Reopened(resp.Agents[0].Name, resp.Agents[0].Board, "", true)
	case resp.Lost != nil:
		note = deliverytext.Lost(resp.Lost.Name, resp.Lost.Board)
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

// turnStart reports a turn starting and adds to it, before the model runs, the messages
// that waited for it (in focused mode, the quiet ones too). wake marks a prompt that
// hands back a waiting hook's wake text: the wake itself, which must not count as the
// session's next event. A daemon from before turn_start is sent prompt instead.
func (h hookCall) turnStart(ctx context.Context, wake bool) error {
	if ticket := h.a.waitingTicket(ticketInPrompt(h.in.Prompt)); ticket != "" {
		// A session aboard swarm up started with its launch ticket in its first prompt
		// (Codex): the hook input carries the prompt, so the ticket binds the session to
		// its agent before the turn starts, whatever process runs the hook.
		req := h.request(delivery.OpRegister)
		req.Launch = ticket
		if _, err := h.a.callDaemon(ctx, req); err != nil {
			return err
		}
	}
	req := h.request(delivery.OpTurnStart)
	req.Wake, req.Started = wake, h.started
	resp, err := h.a.callDaemon(ctx, req)
	var e *Error
	if errors.As(err, &e) && e.Code == "invalid_request" {
		req.Op = delivery.OpPrompt
		resp, err = h.a.callDaemon(ctx, req)
	}
	if err != nil {
		return err
	}
	text := strings.TrimSpace(resp.Nudge + "\n\n" + resp.Bundle)
	if !wake {
		// Never with a wake: the line is quiet, for a turn the person started.
		text += "\n\n" + h.addedNote(ctx, false)
	}
	return h.addContext(strings.TrimSpace(text), "UserPromptSubmit")
}

// tool adds to a busy turn, at a tool boundary, the owner's messages and a notice of the
// other messages waiting. It never blocks or changes the tool call.
func (h hookCall) tool(ctx context.Context) error {
	req := h.request(delivery.OpBoundary)
	req.Started = h.started
	if h.peerBoundary {
		req.Capabilities = []string{"tool-boundary", "midturn-peer"}
	}
	resp, err := h.a.callDaemon(ctx, req)
	if err != nil {
		return err
	}
	if err := h.addContext(strings.TrimSpace(resp.Bundle+"\n\n"+resp.Notice), "PostToolUse"); err != nil {
		return err
	}
	if resp.HandoffID != "" {
		receipt := h.request(delivery.OpReceived)
		receipt.HandoffID, receipt.Boot, receipt.TurnID = resp.HandoffID, resp.Boot, resp.TurnID
		_, err := h.a.callDaemon(ctx, receipt)
		return err
	}
	return nil
}

// addContext prints text as the hook's additionalContext, for the event the hook input
// names, else fallback. It prints nothing for no text.
func (h hookCall) addContext(text, fallback string) error {
	if text == "" {
		return nil
	}
	event := h.in.HookEventName
	if event == "" {
		event = fallback
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

// runsAboard matches a shell command that runs aboard as a command: at the start, or
// after a separator, a pipe, a subshell or a command substitution, optionally behind
// variable assignments, a wrapper such as env, or a path ending in /aboard. A command
// that only names aboard, such as a path to a project folder called aboard, doesn't
// match, so the harness's allow rules keep applying to it.
var runsAboard = regexp.MustCompile("(^|[;&|(`\\n]|\\$\\()\\s*" +
	`(?:(?:[A-Za-z_][A-Za-z0-9_]*=\S*|env|command|exec|nohup|time)\s+)*` +
	`(?:\S*/)?aboard(?:$|[\s;&|)])`)

// envName is what a variable name written into an environment file may be.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// markSubagent marks a shell command a subagent is about to run, when it runs
// aboard, by prefixing it with "export ABOARD_SUBAGENT=<agent id>; ", so aboard knows
// the command isn't the parent's and refuses to act as the parent. The tool's other
// input is kept, and no permission decision is given, so the harness's own permission
// rules still decide. Any other command, and every command of the main conversation,
// gets no output and runs as it is.
func (h hookCall) markSubagent() error {
	var input map[string]json.RawMessage
	var command string
	readable := h.in.AgentID != "" && len(h.in.ToolInput) > 0 &&
		json.Unmarshal(h.in.ToolInput, &input) == nil && input["command"] != nil &&
		json.Unmarshal(input["command"], &command) == nil
	if !readable || !runsAboard.MatchString(command) {
		return nil
	}
	prefix := "export " + harness.SubagentEnv + "=" + harness.ShellWord(h.in.AgentID) + "; "
	if strings.HasPrefix(command, prefix) {
		return nil
	}
	raw, err := json.Marshal(prefix + command)
	if err != nil {
		return fmt.Errorf("encode the command: %w", err)
	}
	input["command"] = raw
	event := h.in.HookEventName
	if event == "" {
		event = "PreToolUse"
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName string                     `json:"hookEventName"`
			UpdatedInput  map[string]json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.UpdatedInput = input
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
	var completion delivery.Request
	continued := false
	h.completion, h.continued = &completion, &continued
	defer func() {
		if !continued && completion.TurnID != 0 {
			h.completeTurn(ctx, completion)
		}
	}()
	if h.harness == "codex" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
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
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		code, done := h.waitOn(conn, req)
		stop()
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
		case resp.Event == delivery.EventWaiting:
			if h.completion != nil && resp.TurnID != 0 && resp.Boot != "" {
				*h.completion = req
				h.completion.Op, h.completion.TurnID, h.completion.Boot = delivery.OpTurnEnd, resp.TurnID, resp.Boot
			}
		case resp.Error != nil:
			h.a.hookNote(resp.Error.Message)
			return 0, true
		case resp.Event == delivery.EventDeliver:
			if h.harness == "codex" {
				if err := json.NewEncoder(h.a.env.Stdout).Encode(struct {
					Decision string `json:"decision"`
					Reason   string `json:"reason"`
				}{Decision: "block", Reason: resp.Bundle}); err != nil {
					return 0, true
				}
				_ = delivery.WriteFrame(conn, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpReceived, HandoffID: resp.HandoffID})
				if h.continued != nil {
					*h.continued = true
				}
				return 0, true
			}
			_ = delivery.WriteFrame(conn, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpReceived, HandoffID: resp.HandoffID})
			_, _ = io.WriteString(h.a.env.Stderr, resp.Bundle+"\n\nAboard delivery: new messages for this session.\n")
			if h.continued != nil {
				*h.continued = true
			}
			return exitWake, true
		case resp.Event == delivery.EventRelease:
			return 0, true
		}
	}
}

// Missing completion keeps the peer cap charged; it never starts another daemon.
func (h hookCall) completeTurn(parent context.Context, req delivery.Request) {
	p, err := h.a.paths()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Second)
	defer cancel()
	conn, err := control.Dial(ctx, p.socket())
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	req.V = delivery.ProtocolVersion
	if delivery.WriteFrame(conn, req) == nil {
		var response delivery.Response
		_ = delivery.ReadFrame(bufio.NewReader(conn), &response)
	}
}
