package delivery

import (
	"context"
	"errors"
	"net"
	"time"
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

// Class is derived from every admitted message, never from rendered text.
type Class string

// Handoff classes distinguish owner-only payloads from mixed or unknown ones.
const (
	ClassOwnerOnly      Class = "owner_only"
	ClassMixed          Class = "mixed"
	CapabilityHandoffV1       = "handoff-v1"
)

// ErrExtensionOutdated means the connected extension cannot receive combined handoffs.
var ErrExtensionOutdated = errors.New("the extension does not support combined handoffs")

// Handover is one bundle for one session.
type Handover struct {
	SessionID string
	HandoffID string
	Class     Class
	// ID is the first delivery the bundle carries, which a harness extension names when
	// it confirms the bundle.
	ID     int64
	Bundle string
	// Waiter is the hook or extension connection waiting for this session to be given a
	// bundle, or nil.
	Waiter Waiter
}

// Waiter is a connection waiting while its session is idle: a stop hook's, or a harness
// extension's. Its open connection is the idle signal.
type Waiter interface {
	// Deliver sends the bundle, the delivery id first in it, and returns once the other
	// end has it. It returns ErrBusy if the connection went away first.
	Deliver(ctx context.Context, id int64, bundle string) error
	// Release tells a hook to exit without a bundle. An extension's connection stays
	// open, so it is told nothing.
	Release()
}

// HandoffWaiter accepts an immutable combined handoff or an explicitly classified
// legacy delivery. Existing one-seat waiters need only implement Waiter.
type HandoffWaiter interface {
	DeliverHandoff(context.Context, Handover) error
}

// Server is one Aboard server, reached with this machine's logins.
type Server interface {
	// Follow streams the heads of the human's boards and calls head for each, and for
	// each move of the read position of one of the human's agents (Head.Read), whoever
	// acknowledged, until ctx ends or the connection fails. connected is called once the
	// stream is open.
	Follow(ctx context.Context, connected func(), head func(Head)) error
	// Inbox returns the agent's unread messages, oldest first, its read position, and
	// its delivery mode as the server holds it, read together; mode is nil from a server
	// that doesn't hold delivery modes.
	Inbox(ctx context.Context, agent AgentRef) (msgs []Message, cursor int, mode *HeldMode, err error)
	// Ack moves the agent's read position up to seq.
	Ack(ctx context.Context, agent AgentRef, upTo int) error
	// SetPresence reports what the agent's session is doing, and the agent's delivery
	// mode, so senders can tell when a message will reach it.
	SetPresence(ctx context.Context, agent AgentRef, p Presence, mode Mode) error
}

// Journal records sessions, bindings and every delivery durably. It never stores
// tokens or message bodies.
type Journal interface {
	SaveSession(ctx context.Context, s SessionRecord) error
	Sessions(ctx context.Context) ([]SessionRecord, error)
	Bind(ctx context.Context, b Binding) error
	BindGeneration(ctx context.Context, b Binding, advance bool) (Binding, error)
	PrepareHandoff(ctx context.Context, manifest HandoffManifest) (HandoffManifest, error)
	ConfirmHandoff(ctx context.Context, id string, session SessionKey, boot string, surviving []AgentKey, at time.Time) ([]Delivery, error)
	Handoffs(ctx context.Context) ([]HandoffManifest, error)
	Bindings(ctx context.Context) ([]Binding, error)
	// ResolveIdentity attaches legacy state to the seat proved by its own token.
	// It never changes rows that already name a different seat.
	ResolveIdentity(ctx context.Context, old, resolved AgentRef) error
	// SetMode records an agent's delivery mode: one set on this machine, or the last one
	// read from a server that holds it.
	SetMode(ctx context.Context, agent AgentRef, mode Mode) error
	// Modes returns every agent's recorded delivery mode.
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

// Tickets holds launch tickets: what aboard swarm up gives a session it starts, so the
// session's first contact with the daemon binds it to its agent with no join line.
type Tickets interface {
	// Take returns the agent a ticket names and removes the ticket, so it works once. ok
	// is false for a ticket that doesn't exist or was already taken.
	Take(ticket string) (agent AgentRef, ok bool, err error)
}

// Processes reports whether a harness process still runs, so a session whose harness
// died without running its end hook is closed.
type Processes interface {
	Alive(p Process) bool
	// StartTime returns when a running process started, in the system's own units, for
	// a client that names only its process id. ok is false when it can't be read.
	StartTime(pid int) (start int64, ok bool)
}
