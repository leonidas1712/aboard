package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

var _ api.Responses = (*Store)(nil)

// SavedResponse finds the response saved for an idempotency key in a caller's scope,
// and reports whether there was one.
func (s *Store) SavedResponse(ctx context.Context, scope, key string) (api.SavedResponse, bool, error) {
	var r api.SavedResponse
	err := s.read(ctx, func(t *tx) error {
		return t.queryRow("SELECT request_hash, status, content_type, body FROM idempotency WHERE scope = ? AND key = ?", scope, key).
			Scan(&r.RequestHash, &r.Status, &r.ContentType, &r.Body)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.SavedResponse{}, false, nil
	}
	if err != nil {
		return api.SavedResponse{}, false, fmt.Errorf("read saved response: %w", err)
	}
	return r, true, nil
}

// SaveResponse stores the response to an idempotent write. If one is already saved for
// the key, the first one is kept, so every retry replays the same answer.
func (s *Store) SaveResponse(ctx context.Context, scope, key string, r api.SavedResponse) error {
	at := s.clk.Now().UTC().Format(time.RFC3339)
	err := s.write(ctx, func(t *tx) error {
		return t.exec("INSERT INTO idempotency (scope, key, request_hash, status, content_type, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
			scope, key, r.RequestHash, r.Status, r.ContentType, r.Body, at)
	})
	if err != nil {
		return fmt.Errorf("save response: %w", err)
	}
	return nil
}
