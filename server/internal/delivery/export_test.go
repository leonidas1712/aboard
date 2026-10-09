package delivery

import "context"

// JoinHooks stop a join at its points of contention, for tests: SeatsRead runs once a
// join has read the session's seats, before it checks the server; Waiting runs when a
// join finds the session's turn taken and is about to wait for it.
type JoinHooks struct {
	SeatsRead func()
	Waiting   func()
}

// WithJoinHooks sets the hooks a daemon run with cfg calls.
func WithJoinHooks(cfg *Config, h JoinHooks) {
	cfg.joinHooks = &joinHooks{seatsRead: h.SeatsRead, waiting: h.Waiting}
}

// PublishQueueIntent exercises publication with real durable reporter state.
func PublishQueueIntent(ctx context.Context, j QueueReportJournal, srv QueueReportServer, state QueueReporter, desired []QueuedMessage) (bool, error) {
	return publishQueueIntent(ctx, j, srv, state, desired)
}
