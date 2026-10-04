// Package launcher is how aboard swarm up starts, checks and stops agents' sessions on
// this machine: one small interface that the built-in launchers (tmux, headless) and the
// external aboard-launcher-<name> commands all answer, and the JSON protocol the external
// ones speak (spec/launcher.md). A launcher only hosts sessions; the board, the seats and
// the identity in a session's environment are aboard's.
package launcher

import (
	"context"
	"fmt"
)

// ProtocolVersion is the version every launcher request and response carries.
const ProtocolVersion = 1

// Run modes a launcher may start.
const (
	// ModeInteractive is a terminal session a person can watch and type in.
	ModeInteractive = "interactive"
	// ModeHeadless is a background process with no terminal.
	ModeHeadless = "headless"
)

// Operations of the protocol.
const (
	OpInfo   = "info"
	OpStart  = "start"
	OpStatus = "status"
	OpStop   = "stop"
)

// State is whether a session runs.
type State string

// States a launcher reports.
const (
	Running State = "running"
	Exited  State = "exited"
	// Unknown means the launcher can't tell, such as when its host isn't reachable.
	Unknown State = "unknown"
)

// Info is what a launcher is: its name and the run modes it starts.
type Info struct {
	Name  string   `json:"name"`
	Modes []string `json:"modes"`
}

// Supports reports whether the launcher starts sessions in mode.
func (i Info) Supports(mode string) bool {
	for _, m := range i.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// StartRequest is one session to start.
type StartRequest struct {
	// Swarm groups the sessions of one board on this machine.
	Swarm string `json:"swarm"`
	// Agent names the session within its swarm.
	Agent   string `json:"agent"`
	Harness string `json:"harness"`
	Mode    string `json:"mode"`
	// Argv is the command to run, never through a shell.
	Argv []string `json:"argv"`
	// Env holds the variables the session must have on top of the launcher's own
	// environment, the agent's identity among them.
	Env map[string]string `json:"env"`
	// Dir is the absolute folder to run it in.
	Dir string `json:"dir"`
}

// Started is a session a launcher started.
type Started struct {
	// Handle is the launcher's own reference to the session, passed back unchanged.
	Handle string `json:"handle"`
	// Attach is a line a person runs to watch the session, or empty.
	Attach string `json:"attach,omitempty"`
	// PID is the session's main process, when the launcher knows it.
	PID int `json:"pid,omitempty"`
}

// Ref names a session a launcher started.
type Ref struct {
	Swarm  string `json:"swarm"`
	Agent  string `json:"agent"`
	Handle string `json:"handle"`
}

// Launcher starts, checks and stops sessions. Every implementation passes the kit in
// launchertest.
type Launcher interface {
	// Info says what the launcher is.
	Info(ctx context.Context) (Info, error)
	// Start starts a session. It fails with code already_running when the swarm has a
	// running session for the agent.
	Start(ctx context.Context, req StartRequest) (Started, error)
	// Status reports whether the session runs.
	Status(ctx context.Context, ref Ref) (State, error)
	// Stop ends the session and answers Exited once it is gone; stopping an ended
	// session is no error. The swarm's last session stopping removes what the launcher
	// made for the swarm.
	Stop(ctx context.Context, ref Ref) (State, error)
}

// Asker is a launcher that can also tell when a running session waits on the person,
// such as a harness asking a question in its window as it starts. swarm up says so
// while it waits for the session's seat.
type Asker interface {
	// Blocked reports whether the session runs but waits for the person to answer
	// something in its window.
	Blocked(ctx context.Context, ref Ref) (bool, error)
}

// Error is a launcher's error, with a stable code, in the shape every Aboard error has.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
	// Stderr is what an external launcher wrote to standard error, for people.
	Stderr string `json:"-"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Errorf returns an *Error with a code, a hint and a formatted message.
func Errorf(code, hint, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Hint: hint}
}

// Request is one message to an external launcher, on its standard input.
type Request struct {
	V       int               `json:"v"`
	Op      string            `json:"op"`
	Swarm   string            `json:"swarm,omitempty"`
	Agent   string            `json:"agent,omitempty"`
	Harness string            `json:"harness,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Argv    []string          `json:"argv,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"dir,omitempty"`
	Handle  string            `json:"handle,omitempty"`
}

// Response is one external launcher's answer, on its standard output.
type Response struct {
	V      int      `json:"v"`
	Name   string   `json:"name,omitempty"`
	Modes  []string `json:"modes,omitempty"`
	Handle string   `json:"handle,omitempty"`
	Attach string   `json:"attach,omitempty"`
	PID    int      `json:"pid,omitempty"`
	State  State    `json:"state,omitempty"`
	// Blocked is true on a status answer when the session runs but waits for the
	// person, as far as the launcher can tell.
	Blocked bool   `json:"blocked,omitempty"`
	Error   *Error `json:"error,omitempty"`
}
