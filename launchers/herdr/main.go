// Command aboard-launcher-herdr is Aboard's launcher for herdr, the terminal workspace
// manager for coding agents: aboard swarm up --launcher herdr runs it to start each
// agent's session in a herdr pane. It speaks the launcher protocol (spec/launcher.md):
// one JSON request on standard input, one JSON response on standard output.
//
// Each swarm gets a herdr session of its own (herdr --session <swarm>), with its own
// server, socket and session file, so the person's running herdr workspaces are never
// touched. The launcher starts that session's server in the background when the swarm's
// first agent starts, opens one tab per agent through herdr's socket API (layout.apply,
// which runs an argument vector with its own environment, folder and label, never
// through a shell), and stops the server and removes the session once the swarm's last
// agent stops. A person watches with "herdr session attach <swarm>". herdr reports each
// agent's state on its own; status passes on one of them, "blocked" (the agent waits on
// the person, such as a question as it starts), so swarm up can say so.
//
// It uses only the standard library and doesn't import Aboard's code: it is an example of
// a launcher anyone can write in any language.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// protocol is the launcher protocol version this launcher speaks.
const protocol = 1

type request struct {
	V       int               `json:"v"`
	Op      string            `json:"op"`
	Swarm   string            `json:"swarm"`
	Agent   string            `json:"agent"`
	Harness string            `json:"harness"`
	Mode    string            `json:"mode"`
	Argv    []string          `json:"argv"`
	Env     map[string]string `json:"env"`
	Dir     string            `json:"dir"`
	Handle  string            `json:"handle"`
}

type response struct {
	V      int      `json:"v"`
	Name   string   `json:"name,omitempty"`
	Modes  []string `json:"modes,omitempty"`
	Handle string   `json:"handle,omitempty"`
	Attach string   `json:"attach,omitempty"`
	State  string   `json:"state,omitempty"`
	// Blocked is herdr's agent_status "blocked" for a running pane: its agent waits on
	// the person.
	Blocked bool      `json:"blocked,omitempty"`
	Error   *protoErr `json:"error,omitempty"`
}

type protoErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e *protoErr) Error() string { return e.Code + ": " + e.Message }

func fail(code, hint, format string, args ...any) *protoErr {
	return &protoErr{Code: code, Message: fmt.Sprintf(format, args...), Hint: hint}
}

func main() {
	resp := handle(context.Background(), os.Stdin)
	resp.V = protocol
	_ = json.NewEncoder(os.Stdout).Encode(resp)
	if resp.Error != nil {
		os.Exit(1)
	}
}

func handle(ctx context.Context, in io.Reader) response {
	var req request
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&req); err != nil {
		return response{Error: fail("invalid_request", "Send one JSON object.", "The request isn't JSON: %v", err)}
	}
	if req.V != protocol {
		return response{Error: fail("launcher_protocol_mismatch", "Use an aboard that speaks launcher protocol 1.",
			"This herdr launcher speaks launcher protocol %d; the request is version %d.", protocol, req.V)}
	}
	var (
		resp response
		err  error
	)
	switch req.Op {
	case "info":
		return response{Name: "herdr", Modes: []string{"interactive"}}
	case "start":
		resp, err = start(ctx, req)
	case "status":
		resp, err = status(ctx, req)
	case "stop":
		resp, err = stop(ctx, req)
	default:
		err = fail("invalid_request", "Use info, start, status or stop.", "The herdr launcher has no op %q.", req.Op)
	}
	var pe *protoErr
	if errors.As(err, &pe) {
		return response{Error: pe}
	}
	if err != nil {
		return response{Error: fail("start_failed", "Look at herdr's log in its session folder.", "%v", err)}
	}
	return resp
}

// sessionName is what a herdr session may be called.
var sessionName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func start(ctx context.Context, req request) (response, error) {
	switch {
	case !sessionName.MatchString(req.Swarm) || req.Swarm == "default" || strings.Trim(req.Swarm, ".") == "":
		return response{}, fail("invalid_request", "Use letters, digits, dots, dashes and underscores.", "%q can't name a herdr session.", req.Swarm)
	case req.Agent == "" || len(req.Argv) == 0 || req.Dir == "":
		return response{}, fail("invalid_request", "Send agent, argv and dir.", "A start request needs an agent, a command and a folder.")
	case req.Mode != "interactive":
		return response{}, fail("mode_unsupported", "Use the headless launcher for headless agents.", "The herdr launcher starts only interactive sessions, not %q.", req.Mode)
	}
	h := herdr{session: req.Swarm}
	if err := h.ensureServer(ctx); err != nil {
		return response{}, err
	}
	panes, err := h.panes(ctx)
	if err != nil {
		return response{}, err
	}
	for _, p := range panes {
		if p.Label == req.Agent {
			return response{}, fail("already_running", "Stop it first with aboard swarm down "+req.Agent+".",
				"%s already runs in pane %s of herdr session %s.", req.Agent, p.PaneID, req.Swarm)
		}
	}
	ws, shell, err := h.workspace(ctx, req.Dir)
	if err != nil {
		return response{}, err
	}
	var applied struct {
		Layout struct {
			Root struct {
				PaneID string `json:"pane_id"`
			} `json:"root"`
		} `json:"layout"`
	}
	env := map[string]string{}
	for k, v := range req.Env {
		env[k] = v
	}
	err = h.call(ctx, "layout.apply", map[string]any{
		"workspace_id": ws, "tab_label": req.Agent, "focus": false,
		"root": map[string]any{"type": "pane", "label": req.Agent, "cwd": req.Dir, "command": req.Argv, "env": env},
	}, &applied)
	if err != nil {
		return response{}, err
	}
	if shell != "" {
		// The shell a new workspace opens with: the agent's tab keeps the workspace now.
		_ = h.call(ctx, "pane.close", map[string]any{"pane_id": shell}, nil)
	}
	pane := applied.Layout.Root.PaneID
	if pane == "" {
		return response{}, fail("start_failed", "Look at herdr's log in its session folder.", "herdr opened the tab but named no pane.")
	}
	return response{Handle: req.Swarm + "/" + pane, Attach: "herdr session attach " + req.Swarm}, nil
}

func status(ctx context.Context, req request) (response, error) {
	h, pane, err := parse(req.Handle)
	if err != nil {
		return response{}, err
	}
	if !h.answers(ctx) {
		return response{State: "exited"}, nil
	}
	// herdr watches the agent in each pane, and says "blocked" when it waits on the
	// person, such as a question as it starts.
	var got struct {
		Pane struct {
			AgentStatus string `json:"agent_status"`
		} `json:"pane"`
	}
	err = h.call(ctx, "pane.get", map[string]any{"pane_id": pane}, &got)
	var he *herdrErr
	switch {
	case err == nil:
		return response{State: "running", Blocked: got.Pane.AgentStatus == "blocked"}, nil
	case errors.As(err, &he) && strings.HasSuffix(he.Code, "not_found"):
		return response{State: "exited"}, nil
	}
	return response{State: "unknown"}, nil
}

func stop(ctx context.Context, req request) (response, error) {
	h, pane, err := parse(req.Handle)
	if err != nil {
		return response{}, err
	}
	if !h.answers(ctx) {
		return response{State: "exited"}, nil
	}
	// herdr hangs up the pane's processes, then terminates and kills them, before it
	// answers.
	err = h.call(ctx, "pane.close", map[string]any{"pane_id": pane}, nil)
	var he *herdrErr
	if err != nil && (!errors.As(err, &he) || !strings.HasSuffix(he.Code, "not_found")) {
		return response{}, err
	}
	// A herdr workspace closes with its last pane; with none left, the swarm is over.
	var list struct {
		Workspaces []struct {
			ID string `json:"workspace_id"`
		} `json:"workspaces"`
	}
	if err := h.call(ctx, "workspace.list", map[string]any{}, &list); err == nil && len(list.Workspaces) == 0 {
		h.remove(ctx)
	}
	return response{State: "exited"}, nil
}

func parse(handle string) (herdr, string, error) {
	session, pane, ok := strings.Cut(handle, "/")
	if !ok || !sessionName.MatchString(session) || pane == "" {
		return herdr{}, "", fail("invalid_request", "Pass the handle start returned.", "%q is not a herdr launcher handle.", handle)
	}
	return herdr{session: session}, pane, nil
}

// herdr is one herdr session, reached through its socket.
type herdr struct{ session string }

// dir is the session's folder: herdr keeps a named session's socket, session file and
// logs in <config>/sessions/<name>, its config folder being $XDG_CONFIG_HOME/herdr or
// ~/.config/herdr.
func (h herdr) dir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "herdr", "sessions", h.session)
}

func (h herdr) socket() string { return filepath.Join(h.dir(), "herdr.sock") }

// command is the herdr program: ABOARD_HERDR, else herdr from the PATH.
func command() (string, error) {
	name := os.Getenv("ABOARD_HERDR")
	if name == "" {
		name = "herdr"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fail("host_unavailable", "Install herdr (see https://github.com/naaive/herdr), or use the tmux launcher.",
			"herdr isn't installed: %s isn't on the PATH.", name)
	}
	return path, nil
}

// herdrVars are the variables a herdr pane gives its commands. aboard swarm up may run
// in a herdr pane; they would point the swarm's server at the person's own.
var herdrVars = []string{"HERDR_SOCKET_PATH", "HERDR_CLIENT_SOCKET_PATH", "HERDR_SESSION", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID"}

// ensureServer starts the session's server in the background unless it answers.
func (h herdr) ensureServer(ctx context.Context) error {
	if h.answers(ctx) {
		return nil
	}
	path, err := command()
	if err != nil {
		return err
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !contains(herdrVars, name) {
			env = append(env, kv)
		}
	}
	if runtime.GOOS == "darwin" {
		// As herdr does for its own background server: run it in the person's login
		// context, so its panes keep working like tmux's.
		env = append(env, "HERDR_MACOS_SERVER_CONTEXT=user")
	}
	if err := os.MkdirAll(h.dir(), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", h.dir(), err)
	}
	logFile, err := os.OpenFile(filepath.Join(h.dir(), "aboard-launcher.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open the launcher's log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	cmd := &exec.Cmd{
		Path: path, Args: []string{path, "--session", h.session, "server"}, Env: env,
		Stdout: logFile, Stderr: logFile, SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return fail("host_unavailable", "Run herdr --session "+h.session+" server in a terminal to see why.", "Couldn't start herdr's server: %v", err)
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(15 * time.Second)
	for !h.answers(ctx) {
		if time.Now().After(deadline) {
			return fail("host_unavailable", "Look at "+filepath.Join(h.dir(), "aboard-launcher.log")+".",
				"herdr's server for session %s didn't answer within 15 seconds.", h.session)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (h herdr) answers(ctx context.Context) bool {
	return h.call(ctx, "ping", map[string]any{}, nil) == nil
}

// pane is what the launcher reads of herdr's PaneInfo.
type pane struct {
	PaneID string `json:"pane_id"`
	Label  string `json:"label"`
}

func (h herdr) panes(ctx context.Context) ([]pane, error) {
	var list struct {
		Panes []pane `json:"panes"`
	}
	if err := h.call(ctx, "pane.list", map[string]any{}, &list); err != nil {
		return nil, err
	}
	return list.Panes, nil
}

// workspace returns the session's workspace, creating it in dir when there is none. A
// new workspace opens with a shell, whose pane is returned so it can be closed once the
// agent's tab is open.
func (h herdr) workspace(ctx context.Context, dir string) (id, shell string, err error) {
	var list struct {
		Workspaces []struct {
			ID string `json:"workspace_id"`
		} `json:"workspaces"`
	}
	if err := h.call(ctx, "workspace.list", map[string]any{}, &list); err != nil {
		return "", "", err
	}
	if len(list.Workspaces) > 0 {
		return list.Workspaces[0].ID, "", nil
	}
	var created struct {
		Workspace struct {
			ID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane pane `json:"root_pane"`
	}
	if err := h.call(ctx, "workspace.create", map[string]any{"cwd": dir, "label": h.session, "focus": false}, &created); err != nil {
		return "", "", err
	}
	return created.Workspace.ID, created.RootPane.PaneID, nil
}

// remove stops the session's server, which ends what still runs in it, and deletes the
// session's folder, as herdr session stop and delete do.
func (h herdr) remove(ctx context.Context) {
	_ = h.call(ctx, "server.stop", map[string]any{}, nil)
	deadline := time.Now().Add(15 * time.Second)
	for h.answers(ctx) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !h.answers(ctx) {
		_ = os.RemoveAll(h.dir())
	}
}

// herdrErr is an error herdr's API answered with.
type herdrErr struct{ Code, Message string }

func (e *herdrErr) Error() string { return "herdr: " + e.Code + ": " + e.Message }

// call sends one request on the session's socket and decodes the result into out.
func (h herdr) call(ctx context.Context, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", h.socket())
	if err != nil {
		return fmt.Errorf("reach herdr session %s: %w", h.session, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	line, err := json.Marshal(map[string]any{"id": "aboard", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("send %s to herdr: %w", method, err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read herdr's answer to %s: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *herdrErr       `json:"error"`
	}
	if err := json.Unmarshal(reply, &resp); err != nil {
		return fmt.Errorf("read herdr's answer to %s: %w", method, err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("read herdr's answer to %s: %w", method, err)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
