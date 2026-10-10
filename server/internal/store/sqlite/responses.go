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

const responseLifetime = 24 * time.Hour

// SavedResponse finds the response saved for an idempotency key in a caller's scope,
// and reports whether it is still within its 24-hour lifetime.
func (s *Store) SavedResponse(ctx context.Context, scope, key string) (api.SavedResponse, bool, error) {
	var r api.SavedResponse
	found := false
	err := s.read(ctx, func(t *tx) error {
		var at string
		if err := t.queryRow("SELECT request_hash, status, content_type, body, created_at FROM idempotency WHERE scope = ? AND key = ?", scope, key).
			Scan(&r.RequestHash, &r.Status, &r.ContentType, &r.Body, &at); err != nil {
			return err
		}
		var err error
		found, err = responseCurrent(at, s.clk.Now())
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.SavedResponse{}, false, nil
	}
	if err != nil {
		return api.SavedResponse{}, false, fmt.Errorf("read saved response: %w", err)
	}
	if !found {
		return api.SavedResponse{}, false, nil
	}
	return r, found, nil
}

// SaveResponse stores the response to an idempotent write. If one is already saved for
// the key and has not expired, the first one is kept. An expired answer is replaced
// in the same transaction, so the new request has its own retry window.
func (s *Store) SaveResponse(ctx context.Context, scope, key string, r api.SavedResponse) error {
	err := s.write(ctx, func(t *tx) error {
		now := s.clk.Now()
		var at string
		err := t.queryRow("SELECT created_at FROM idempotency WHERE scope = ? AND key = ?", scope, key).Scan(&at)
		if err == nil {
			current, err := responseCurrent(at, now)
			if err != nil || current {
				return err
			}
			if err := t.exec("DELETE FROM idempotency WHERE scope = ? AND key = ?", scope, key); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return t.exec("INSERT INTO idempotency (scope, key, request_hash, status, content_type, body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING",
			scope, key, r.RequestHash, r.Status, r.ContentType, r.Body, now.UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return fmt.Errorf("save response: %w", err)
	}
	return nil
}

func responseCurrent(at string, now time.Time) (bool, error) {
	created, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return false, fmt.Errorf("parse response timestamp: %w", err)
	}
	return now.Before(created.Add(responseLifetime)), nil
}

// PurgeResponses removes expired answers. This is logical deletion; SQLite pages,
// WAL files and migration backups may still hold copies.
func (s *Store) PurgeResponses(ctx context.Context) error {
	err := s.write(ctx, func(t *tx) error {
		if err := purgeResponses(t, "SELECT scope, key, created_at FROM idempotency WHERE unixepoch(created_at) <= ?", "DELETE FROM idempotency WHERE scope = ? AND key = ?", s.clk.Now()); err != nil {
			return err
		}
		if err := purgeResponses(t, "SELECT delegation_id, key, created_at FROM delegated_creations WHERE unixepoch(created_at) <= ?", "DELETE FROM delegated_creations WHERE delegation_id = ? AND key = ?", s.clk.Now()); err != nil {
			return err
		}
		return t.purgeApprovalCapsules(s.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	})
	if err != nil {
		return fmt.Errorf("purge expired responses: %w", err)
	}
	return nil
}

func purgeResponses(t *tx, selectSQL, deleteSQL string, now time.Time) error {
	// SQL narrows the candidates to their whole second; Go checks the exact expiry,
	// including subsecond timestamps and older rows without fractional seconds.
	rows, err := t.tx.QueryContext(t.ctx, selectSQL, now.Add(-responseLifetime).Unix())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var expired [][2]string
	for rows.Next() {
		var scope, key, at string
		if err := rows.Scan(&scope, &key, &at); err != nil {
			return err
		}
		current, err := responseCurrent(at, now)
		if err != nil {
			return err
		}
		if !current {
			expired = append(expired, [2]string{scope, key})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, key := range expired {
		if err := t.exec(deleteSQL, key[0], key[1]); err != nil {
			return err
		}
	}
	return nil
}
