package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

func cleanBlobs(ctx context.Context, svc *board.Service, clk clock.Clock, log *slog.Logger) error {
	for {
		if _, err := svc.SweepBlobs(ctx); err != nil && ctx.Err() == nil {
			log.Error("sweep unreferenced file bytes", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(time.Hour):
		}
	}
}
