package sqlitejournal

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// ReadSessionBoots reads lifecycle evidence without changing an older daemon's schema.
func ReadSessionBoots(ctx context.Context, path string) ([]delivery.SessionRecord, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(2000)"}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	if version < 1 {
		return nil, nil
	}
	folder := "folder"
	if version < 16 {
		folder = "''"
	}
	rows, err := db.QueryContext(ctx, "SELECT harness, session_id, boot, open, "+folder+" FROM sessions")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []delivery.SessionRecord
	for rows.Next() {
		var record delivery.SessionRecord
		if err := rows.Scan(&record.Key.Harness, &record.Key.ID, &record.Boot, &record.Open, &record.Folder); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
