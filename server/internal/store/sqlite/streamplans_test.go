package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestStreamQueriesAvoidScanningUnrelatedMembers(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "aboard.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, query := range []string{
		"SELECT board_id FROM members WHERE human_id = ? AND kind = 'human' AND status = 'active'",
		"SELECT " + memberColumns + " FROM members WHERE board_id = ? ORDER BY rowid",
		"SELECT COUNT(DISTINCT human_id) FROM members WHERE board_id = ? AND kind = 'agent'",
	} {
		t.Run(query, func(t *testing.T) {
			rows, err := st.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, "selected")
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
				if strings.Contains(detail, "SCAN members") || strings.Contains(detail, "USE TEMP B-TREE") {
					t.Errorf("stream query visits unrelated members or sorts them: %s", detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
