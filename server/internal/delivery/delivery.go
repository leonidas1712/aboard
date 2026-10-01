// Package delivery puts board messages into agent sessions that are already open. It
// holds the delivery daemon's core: which sessions are open, which agent each is bound
// to, the journal of every bundle handed to a harness, and the rules for when a bundle is
// handed over, confirmed and acknowledged. Harnesses, servers, the journal's storage and
// the control socket are reached only through the ports declared in ports.go.
package delivery

import (
	"errors"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// Harness names.
const (
	HarnessClaudeCode = "claude-code"
	HarnessCodex      = "codex"
)

// SessionKey identifies one session: the harness and the harness's own session id.
type SessionKey struct {
	Harness string `json:"harness"`
	ID      string `json:"id"`
}

// String is the form used in ABOARD_SESSION and in output: "claude-code:<id>".
func (k SessionKey) String() string { return k.Harness + ":" + k.ID }

// ParseSessionKey reads "harness:id".
func ParseSessionKey(s string) (SessionKey, bool) {
	h, id, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || h == "" || id == "" {
		return SessionKey{}, false
	}
	return SessionKey{Harness: h, ID: id}, true
}

// AgentRef names one agent: its server, its board and its name there. An agent belongs
// to exactly one board.
type AgentRef struct {
	Server string `json:"server"`
	Board  string `json:"board"`
	Name   string `json:"name"`
}

// Message is one board message as the delivery text shows it.
type Message = deliverytext.Message

// Head is a board's latest sequence number, as a server's stream reports it.
type Head struct {
	Board string
	Seq   int
}

// State is where a delivery is in its life.
type State string

// Delivery states.
const (
	// StatePending waits for the session to take it.
	StatePending State = "pending"
	// StateHanded was given to the harness and waits for confirmation.
	StateHanded State = "handed"
	// StateConfirmed was received by the session and is acknowledged next.
	StateConfirmed State = "confirmed"
	// StateDone is acknowledged on the server.
	StateDone State = "done"
	// StateRetry failed for a passing reason and is tried again at RetryAt.
	StateRetry State = "retry"
	// StateHeld waits because no open session is bound to the agent.
	StateHeld State = "held"
	// StateAttention stopped after MaxAttempts failed attempts.
	StateAttention State = "attention"
	// StateSkipped can't be delivered automatically; the read position moves past it.
	StateSkipped State = "skipped"
)

// Delivery is one agent's part of a bundle handed to a harness. A bundle carries one
// delivery for each agent bound to the session that has messages in it.
type Delivery struct {
	ID    int64
	Agent AgentRef
	// Session and Boot are the session the delivery was last handed to.
	Session  SessionKey
	Boot     string
	State    State
	Seqs     []int
	Attempts int
	// Reason is the code of the last failure, such as codex_target_absent.
	Reason    string
	RetryAt   time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionRecord is what the journal keeps of a session.
type SessionRecord struct {
	Key       SessionKey
	Boot      string
	Open      bool
	UpdatedAt time.Time
}

// Binding says which session an agent's messages go to.
type Binding struct {
	Agent   AgentRef
	Session SessionKey
	BoundAt time.Time
}

// Reason codes recorded on failed or skipped deliveries and reported by aboard doctor.
const (
	ReasonTargetAbsent = "codex_target_absent"
	ReasonSubAgent     = "codex_subagent_target"
	ReasonUnauthorized = "unauthorized"
	ReasonHarnessError = "harness_error"
	ReasonTooLarge     = "too_large"
)

// Errors that adapters return, so the daemon can decide what a failure means.
var (
	// ErrBusy means the session can't take a bundle now. It never counts as an attempt.
	ErrBusy = errors.New("session is busy")
	// ErrTargetAbsent means the session no longer exists in its harness.
	ErrTargetAbsent = errors.New("session no longer exists")
	// ErrSubAgent means the session is a sub-agent thread, not a root conversation.
	ErrSubAgent = errors.New("session is a sub-agent thread")
	// ErrUnauthorized means the server rejected the agent's token or the human login.
	ErrUnauthorized = errors.New("token rejected")
	// ErrLoginMissing means there is no human login for the server.
	ErrLoginMissing = errors.New("no human login for this server")
)

// Limits and timings of delivery.
const (
	// BundleLimit is the most text one bundle carries, in bytes.
	BundleLimit = 32 << 10
	// MaxAttempts is how many failed attempts stop a delivery for attention.
	MaxAttempts = 5
	// IdleExit is how long the daemon runs with no open session.
	IdleExit = 10 * time.Minute
	// QueueGather is how long the daemon waits for more messages before handing a
	// bundle to a harness that queues, so messages close together arrive together.
	QueueGather = 2 * time.Second
	// ShutdownGrace is how long in-flight harness calls may run after shutdown starts.
	ShutdownGrace = 5 * time.Second
)

// backoff is the wait before attempt n+1 after n failed attempts: 1, 2, 4 … 60 seconds.
func backoff(attempts int) time.Duration {
	d := time.Second
	for i := 1; i < attempts && d < time.Minute; i++ {
		d *= 2
	}
	return min(d, time.Minute)
}
