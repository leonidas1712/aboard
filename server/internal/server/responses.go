package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

type responsePurger interface {
	PurgeResponses(context.Context) error
}

// cleanResponses removes expired answers at startup and hourly. Reads enforce the
// lifetime independently, so a failed cleanup delays only physical row removal.
func cleanResponses(ctx context.Context, st responsePurger, clk clock.Clock, log *slog.Logger) error {
	for {
		if err := st.PurgeResponses(ctx); err != nil && ctx.Err() == nil {
			log.Error("purge expired responses", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(time.Hour):
		}
	}
}
