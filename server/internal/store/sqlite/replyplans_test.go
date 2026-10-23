package sqlite

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Board badges are read frequently. Each question must look up its replies without
// scanning the whole message history, including messages on other boards.
func TestReplyCountAvoidsScanningMessageHistory(t *testing.T) {
	for _, name := range []string{"new database", "existing database", "index already present"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aboard.db")
			st, err := Open(t.Context(), path, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if name != "new database" {
				if name == "existing database" {
					if _, err := st.db.ExecContext(t.Context(), "DROP INDEX IF EXISTS messages_reply_sender"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := st.db.ExecContext(t.Context(), "PRAGMA user_version = 36"); err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(t.Context(), path, clock.Real{})
				if err != nil {
					t.Fatal(err)
				}
				st = reopened
			}
			rows, err := st.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+needsReplyCountQuery, "board", "reader", "reader", "reader")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(detail, "SCAN r") {
					t.Errorf("reply count scans message history for each question: %s", detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
