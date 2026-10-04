// Package delivery puts board messages into agent sessions that are already open. It
// holds the delivery daemon's core: which sessions are open, which agent each is bound
// to, the journal of every bundle handed to a harness, and the rules for when a bundle is
// handed over, confirmed and acknowledged. Harnesses, servers, the journal's storage and
// the control socket are reached only through the ports declared in ports.go.
package delivery

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
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

// Head is what a server's stream reports about a board: its latest sequence number, or,
// when Read is set, instead that one of the human's agents on it read up to a point.
type Head struct {
	Board string
	Seq   int
	Read  *ReadPosition
}

// ReadPosition is an agent's read position as the server reports it: the server is the
// authority on what an agent has read, whichever client acknowledged.
type ReadPosition struct {
	Agent string
	UpTo  int
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
	// AcceptedAt is when the harness accepted the delivery: a waiting hook took it, its
	// queue took it, or its extension added it. TurnStartedAt is when the session's next
	// turn started after that. Both are zero until then.
	AcceptedAt    time.Time
	TurnStartedAt time.Time
	// Stalled is set when the delivery was handed to an idle session that started no
	// turn within StallAfter. It is reported, never handed again because of it.
	Stalled bool
}

// SessionRecord is what the journal keeps of a session.
type SessionRecord struct {
	Key  SessionKey
	Boot string
	Open bool
	// Process is the harness process the session runs in, or nil if it isn't known.
	Process *Process
	// Lost is the agent another session resumed while this one held it, or nil. The
	// session doesn't take it back when it starts again; it says so instead.
	Lost *AgentRef
	// Turned is true once the session has run a turn, which a harness needs before it
	// can resume the session (Claude Code saves a conversation only from its first turn).
	Turned    bool
	UpdatedAt time.Time
}

// Process is one running process. Start is when it started, in the system's own units,
// so a process id the system has since reused isn't mistaken for it.
type Process struct {
	PID   int   `json:"pid"`
	Start int64 `json:"start"`
}

// Binding says which session an agent's messages go to.
type Binding struct {
	Agent   AgentRef
	Session SessionKey
	BoundAt time.Time
}

// Mode is how an agent's messages reach its session. A person chooses it per agent.
type Mode string

// Delivery modes.
const (
	// ModeFocused, the default, wakes the session only for messages that concern the
	// agent (Concerns); the rest arrive quietly at the start of its next turn.
	ModeFocused Mode = "focused"
	// ModeAll wakes the session for every message.
	ModeAll Mode = "all"
	// ModeHumans wakes the session only for a message from a person; that bundle carries
	// every unread message, peer ones too.
	ModeHumans Mode = "humans"
	// ModeOff delivers nothing; the agent reads its inbox itself.
	ModeOff Mode = "off"
	// ModeAuto is the earlier name of ModeAll. It is still accepted, and read as ModeAll.
	ModeAuto Mode = "auto"
)

// ParseMode reads a mode's name. auto, the earlier name of all, is read as all.
func ParseMode(s string) (Mode, bool) {
	switch m := Mode(s); m {
	case ModeFocused, ModeAll, ModeHumans, ModeOff:
		return m, true
	case ModeAuto:
		return ModeAll, true
	}
	return "", false
}

// Concerns reports whether m concerns the agent called name, so that in focused mode it
// wakes the agent's session: a person sent it, it is addressed to the agent or its role
// (an inbox holds only messages addressed to the agent, its role or everyone, so any
// target but all), it replies to one of the agent's messages, it asks for a reply, or it
// is urgent. A message whose targets aren't known counts as addressed.
func Concerns(m Message, name string) bool {
	return m.FromHuman || m.Urgent || m.ExpectsReply || m.ReplyToFrom == name ||
		len(m.To) == 0 || !slices.Contains(m.To, "all")
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

// SessionError is an adapter's verdict on a session, in its harness's own words: Err is
// ErrTargetAbsent or ErrSubAgent, and Message and Hint are what the person reads.
type SessionError struct {
	Err           error
	Message, Hint string
}

func (e *SessionError) Error() string { return e.Message }

// Unwrap returns Err, so errors.Is sees the kind of failure.
func (e *SessionError) Unwrap() error { return e.Err }

// Limits and timings of delivery.
const (
	// BundleLimit is the most text one bundle carries, in bytes.
	BundleLimit = 32 << 10
	// MaxAttempts is how many failed attempts stop a delivery for attention.
	MaxAttempts = 5
	// IdleExit is how long the daemon runs with no open session.
	IdleExit = 10 * time.Minute
	// LivenessCheck is how often the daemon checks that each open session's harness
	// process still runs.
	LivenessCheck = 5 * time.Second
	// QueueGather is how long the daemon waits for more messages, from the first one that
	// would wake a session, before handing a bundle to any harness, so messages close
	// together wake the session once.
	QueueGather = 2 * time.Second
	// DigestMessages and DigestBytes are how many messages, and how many bytes of them in
	// the delivery format, a bundle carries in full; past either, in focused and humans
	// mode, it carries in full only those that concern the agent and one line for each
	// other (deliverytext.Digest).
	DigestMessages = 10
	DigestBytes    = 8 << 10
	// ShutdownGrace is how long in-flight harness calls may run after shutdown starts.
	ShutdownGrace = 5 * time.Second
	// PresenceRenew is how often the daemon reports an agent's presence again while it
	// holds. A server lets a presence run out after 3 minutes without a report.
	PresenceRenew = time.Minute
	// StallAfter is how long a session handed a bundle while idle may take to start a
	// turn before the delivery counts as stalled. Waking takes the harnesses about a
	// second.
	StallAfter = 10 * time.Second
)

// Presence is what an agent's session is doing, as the daemon reports it to the
// agent's server for the board's members to see.
type Presence string

// Presences the daemon reports. It never reports waiting (the harness waiting for a
// person in the session): neither harness's hooks that Aboard installs say when that
// happens.
const (
	// PresenceWorking means a turn is running in the session.
	PresenceWorking Presence = "working"
	// PresenceIdle means the session is open and waiting for messages.
	PresenceIdle Presence = "idle"
	// PresenceNoSession means no open session is bound to the agent.
	PresenceNoSession Presence = "no_session"
)

// backoff is the wait before attempt n+1 after n failed attempts: 1, 2, 4 … 60 seconds.
func backoff(attempts int) time.Duration {
	d := time.Second
	for i := 1; i < attempts && d < time.Minute; i++ {
		d *= 2
	}
	return min(d, time.Minute)
}
