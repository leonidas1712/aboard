package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestExpiredResponsesAreRemovedWithoutRemovingWorkingRetries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 1, 16, 0, 0, 999999999, time.UTC)
	clk := clock.NewFake(start)
	st, err := Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	answer := api.SavedResponse{RequestHash: "first", Status: 200, ContentType: "application/json", Body: []byte(`{}`)}
	if err := st.SaveResponse(ctx, "owner", "old", answer); err != nil {
		t.Fatal(err)
	}
	// Older versions stored whole-second timestamps.
	if _, err := st.db.ExecContext(ctx, "INSERT INTO idempotency VALUES (?, ?, ?, ?, ?, ?, ?)",
		"other", "legacy", "first", 200, "application/json", []byte(`{}`), start.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	clk.Advance(24*time.Hour - time.Nanosecond)
	if err := st.PurgeResponses(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM idempotency").Scan(&count); err != nil || count != 1 {
		t.Fatalf("before subsecond expiry: rows=%d err=%v, want only the working response", count, err)
	}
	if _, found, err := st.SavedResponse(ctx, "owner", "old"); err != nil || !found {
		t.Fatalf("working response lost: found=%t err=%v", found, err)
	}
	if err := st.SaveResponse(ctx, "owner", "fresh", answer); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Nanosecond)
	if err := st.PurgeResponses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM idempotency").Scan(&count); err != nil || count != 1 {
		t.Fatalf("after expiry: rows=%d err=%v, want only the fresh response", count, err)
	}
	if _, found, err := st.SavedResponse(ctx, "owner", "old"); err != nil || found {
		t.Fatalf("expired response retained: found=%t err=%v", found, err)
	}
	if _, found, err := st.SavedResponse(ctx, "owner", "fresh"); err != nil || !found {
		t.Fatalf("fresh response lost: found=%t err=%v", found, err)
	}
}
