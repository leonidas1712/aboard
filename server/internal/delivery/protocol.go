package delivery

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ProtocolVersion is the version every control socket message carries.
const ProtocolVersion = 1

// MaxFrame is the largest control socket message, in bytes, including its newline.
const MaxFrame = 128 << 10

// Control socket operations.
const (
	// OpRegister registers a session that started, with its boot id.
	OpRegister = "register"
	// OpPrompt reports the session is busy and releases its waiting stop hook.
	OpPrompt = "prompt"
	// OpWait keeps the connection open while the session is idle, until a bundle or a
	// release is sent back.
	OpWait = "wait"
	// OpReceived is sent by a waiting hook once it has the bundle.
	OpReceived = "received"
	// OpTurnEnd reports a turn ended, for a harness whose stop hook doesn't wait.
	OpTurnEnd = "turn_end"
	// OpBoundary asks what a busy session's tool hook adds to the turn: the owner's
	// messages and the waiting notice.
	OpBoundary = "boundary"
	// OpUrgent is OpBoundary's name in earlier builds, still answered the same way.
	OpUrgent = "urgent"
	// OpEnd reports the session closed.
	OpEnd = "end"
	// OpBind binds an agent to the session.
	OpBind = "bind"
	// OpAgents lists the agents bound to the session.
	OpAgents = "agents"
	// OpMode shows an agent's delivery mode, or sets it when the request has one.
	OpMode = "mode"
	// OpStatus reports the daemon's state for aboard doctor.
	OpStatus = "status"
)

// Events sent back on a waiting connection.
const (
	// EventWaiting says the session took the wait: from now on a prompt releases it.
	EventWaiting = "waiting"
	// EventDeliver carries a bundle; the hook answers OpReceived.
	EventDeliver = "deliver"
	// EventRelease tells the hook to exit without a bundle.
	EventRelease = "release"
)

// Request is one message from a hook or the CLI to the daemon.
type Request struct {
	V       int    `json:"v"`
	Op      string `json:"op"`
	Harness string `json:"harness,omitempty"`
	Session string `json:"session,omitempty"`
	// Boot is the session's boot id. Empty means the boot the daemon has on record.
	Boot string `json:"boot,omitempty"`
	// Source is what started the session: startup, resume, clear or compact.
	Source string `json:"source,omitempty"`
	// Resumed marks a wait that reconnects after the daemon went away, so it doesn't
	// count as the session's next event.
	Resumed bool `json:"resumed,omitempty"`
	// Wake marks a prompt that is the bundle a stop hook just woke the session with, not
	// a later event; it doesn't confirm that bundle.
	Wake bool `json:"wake,omitempty"`
	// Started is when the hook process started. A wait from a stop hook that started
	// before the session's latest prompt belongs to an earlier turn, and is released.
	Started time.Time `json:"started,omitzero"`
	Agent   *AgentRef `json:"agent,omitempty"`
	// Mode is the delivery mode an OpMode request sets; empty only shows it.
	Mode Mode `json:"mode,omitempty"`
	// Process is the harness process the request came from, when the caller found it.
	Process *Process `json:"process,omitempty"`
}

// Key returns the session the request is about.
func (r Request) Key() SessionKey { return SessionKey{Harness: r.Harness, ID: r.Session} }

// Response is one message from the daemon.
type Response struct {
	V      int        `json:"v"`
	Event  string     `json:"event,omitempty"`
	Bundle string     `json:"bundle,omitempty"`
	// Notice names waiting messages without their content, in answer to OpBoundary.
	Notice string `json:"notice,omitempty"`
	Boot   string     `json:"boot,omitempty"`
	Agents []AgentRef `json:"agents,omitempty"`
	Status *Status    `json:"status,omitempty"`
	// Mode is the agent's delivery mode, in answer to OpMode; Changed says whether the
	// request changed it.
	Mode    Mode `json:"mode,omitempty"`
	Changed bool `json:"changed,omitempty"`
	// Previous is the agent the session was bound to before an OpBind moved it to
	// another one; nil when the session had no agent or keeps the same one.
	Previous *AgentRef  `json:"previous,omitempty"`
	Error    *WireError `json:"error,omitempty"`
}

// WireError is an error reported over the control socket, in the shape the CLI prints.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e *WireError) Error() string { return e.Code + ": " + e.Message }

// Build identifies the aboard build a daemon or server runs, so a newer command can
// tell an older one apart and replace it.
type Build struct {
	// Version is the release version, compared as a semantic version.
	Version string `json:"version"`
	// Commit and CommitTime name the Git commit the binary was built from, when known.
	Commit     string    `json:"commit,omitempty"`
	CommitTime time.Time `json:"commit_time,omitzero"`
}

// Status is the daemon's state as aboard doctor shows it.
type Status struct {
	PID int `json:"pid"`
	// Build is the daemon's build; a daemon from before builds were reported leaves it
	// empty.
	Build        Build           `json:"build"`
	OpenSessions int             `json:"open_sessions"`
	Servers      []ServerStatus  `json:"servers"`
	Attention    []StatusItem    `json:"attention"`
	Skipped      []StatusItem    `json:"skipped"`
	Agents       []AgentProblem  `json:"agents"`
	Bindings     []BindingStatus `json:"bindings"`
}

// ServerStatus is one server connection.
type ServerStatus struct {
	URL       string `json:"url"`
	Connected bool   `json:"connected"`
	// Problem is login_missing, server_unreachable or empty.
	Problem string `json:"problem,omitempty"`
}

// StatusItem is one delivery aboard doctor lists: stopped for attention, or skipped.
type StatusItem struct {
	ID     int64    `json:"id"`
	Agent  AgentRef `json:"agent"`
	Seqs   []int    `json:"seqs"`
	Reason string   `json:"reason"`
}

// AgentProblem is an agent whose deliveries stopped, such as one with a revoked token.
type AgentProblem struct {
	Agent  AgentRef `json:"agent"`
	Reason string   `json:"reason"`
}

// BindingStatus is one agent and the session it is bound to.
type BindingStatus struct {
	Agent   AgentRef `json:"agent"`
	Session string   `json:"session"`
}

// ErrFrameTooLarge means a control socket message was larger than MaxFrame.
var ErrFrameTooLarge = errors.New("control message too large")

// ReadFrame reads one newline-terminated JSON message of at most MaxFrame bytes.
func ReadFrame(r *bufio.Reader, v any) error {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > MaxFrame {
			return ErrFrameTooLarge
		}
		buf = append(buf, chunk...)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(buf) > 0 {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	if err := json.Unmarshal(bytes.TrimSpace(buf), v); err != nil {
		return fmt.Errorf("decode control message: %w", err)
	}
	return nil
}

// WriteFrame writes v as one JSON line, refusing anything larger than MaxFrame.
func WriteFrame(w io.Writer, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // bundles are full of "<"; escaping them would triple their size
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode control message: %w", err)
	}
	if buf.Len() > MaxFrame {
		return ErrFrameTooLarge
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write control message: %w", err)
	}
	return nil
}
