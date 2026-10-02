package delivery

import (
	"context"
	"net"
)

// Adapter is how the daemon reaches one harness. A new harness is a new Adapter; it
// never reads the journal, moves a read position or holds a token.
type Adapter interface {
	// Harness is the harness's name, such as "codex".
	Harness() string
	// Validate checks that the exact session can receive deliveries. It returns
	// ErrTargetAbsent for a session that doesn't exist and ErrSubAgent for a sub-agent.
	Validate(ctx context.Context, sessionID string) error
	// WaitsForIdle is true when bundles go only to a hook waiting while the session is
	// idle, and false when the harness queues bundles itself.
	WaitsForIdle() bool
	// Hand gives a bundle to the session. confirmed is true when the harness's
	// acceptance alone confirms the session received it. ErrBusy means the session
	// can't take it now and the bundle stays pending.
	Hand(ctx context.Context, h Handover) (confirmed bool, err error)
}

// Handover is one bundle for one session.
type Handover struct {
	SessionID string
	Bundle    string
	// Waiter is the hook waiting for this session to be given a bundle, or nil.
	Waiter Waiter
}

// Waiter is a hook connection waiting while its session is idle. Its open connection is
// the idle signal.
type Waiter interface {
	// Deliver sends the bundle to the hook and returns once the hook has it. It
	// returns ErrBusy if the hook went away first.
	Deliver(ctx context.Context, bundle string) error
	// Release tells the hook to exit without a bundle.
	Release()
}

// Server is one Aboard server, reached with this machine's logins.
type Server interface {
	// Follow streams the heads of the human's boards and calls head for each, until
	// ctx ends or the connection fails. connected is called once the stream is open.
	Follow(ctx context.Context, connected func(), head func(Head)) error
	// Inbox returns the agent's unread messages, oldest first, and its read position.
	Inbox(ctx context.Context, agent AgentRef) (msgs []Message, cursor int, err error)
	// Ack moves the agent's read position up to seq.
	Ack(ctx context.Context, agent AgentRef, upTo int) error
}

// Journal records sessions, bindings and every delivery durably. It never stores
// tokens or message bodies.
type Journal interface {
	SaveSession(ctx context.Context, s SessionRecord) error
	Sessions(ctx context.Context) ([]SessionRecord, error)
	Bind(ctx context.Context, b Binding) error
	Bindings(ctx context.Context) ([]Binding, error)
	// SetMode records an agent's delivery mode.
	SetMode(ctx context.Context, agent AgentRef, mode Mode) error
	// Modes returns every agent's recorded delivery mode. An agent not in it is auto.
	Modes(ctx context.Context) (map[AgentRef]Mode, error)
	// AddDelivery records a new delivery with its messages and returns its id.
	AddDelivery(ctx context.Context, d Delivery) (int64, error)
	// UpdateDelivery saves a delivery's state, session, attempts, reason and retry
	// time. Its messages don't change.
	UpdateDelivery(ctx context.Context, d Delivery) error
	// Deliveries returns the deliveries in any of the given states, oldest first.
	Deliveries(ctx context.Context, states ...State) ([]Delivery, error)
}

// Control is the socket hooks and the CLI use to talk to the daemon. Accept returns
// only connections from this OS user.
type Control interface {
	Accept() (net.Conn, error)
	Close() error
}

// Processes reports whether a harness process still runs, so a session whose harness
// died without running its end hook is closed.
type Processes interface {
	Alive(p Process) bool
}
