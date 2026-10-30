package sqlite

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// Each candidate request must find newer requests in its own sender/thread range,
// without visiting the rest of the board's history. Check both endpoint queries.
func TestRepliesWaitingSearchesOnlyTheSendersThread(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "new database"
		if upgrade {
			name = "schema 37 upgrade"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aboard.db")
			if upgrade {
				old := databaseAt(t, path, 37)
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
			}
			st, err := Open(t.Context(), path, clock.Real{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			for _, query := range []string{
				"SELECT COUNT(*) FROM messages m WHERE " + repliesWaitingWhere,
				messageSelect + " WHERE " + repliesWaitingWhere + " ORDER BY m.at DESC, m.id DESC LIMIT 50",
			} {
				rows, err := st.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, "board", "reader", "reader", "reader")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rows.Close() }()
				found := false
				var plan []string
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					plan = append(plan, detail)
					if strings.HasPrefix(detail, "SEARCH newer ") && strings.Contains(detail, "board_id=? AND sender_id=? AND <expr>=? AND seq>?") {
						found = true
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !found {
					t.Errorf("newer requests must use their sender/thread range; plan:\n%s", strings.Join(plan, "\n"))
				}
			}
		})
	}
}
