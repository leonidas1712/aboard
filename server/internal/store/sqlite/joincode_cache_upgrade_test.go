package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

func TestLifecycleUpgradeDeletesOnlyCachedJoinCodeSecrets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	db := databaseAt(t, path, 21)
	const at = "2026-10-01T16:00:00.000Z"
	for _, query := range []string{
		"INSERT INTO humans (id, name, role, created_at) VALUES ('hum_owner', 'owner', 'admin', '" + at + "')",
		"INSERT INTO boards (id, name, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by) VALUES ('brd_board', 'board', '', '{}', '{}', 0, 'hash', '" + at + "', 'mem_owner')",
		"INSERT INTO members (id, board_id, name, kind, human_id, status, joined_at, access) VALUES ('mem_owner', 'brd_board', 'owner', 'human', 'hum_owner', 'active', '" + at + "', 'admin')",
		"INSERT INTO join_codes (id, board_id, code_digest, role, expires_at, created_at, created_by, revoked_at) VALUES ('jc_active', 'brd_board', 'active-digest', 'member', '2099-01-01T00:00:00.000Z', '" + at + "', 'mem_owner', NULL), ('jc_revoked', 'brd_board', 'revoked-digest', 'member', '2099-01-01T00:00:00.000Z', '" + at + "', 'mem_owner', '" + at + "')",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		key, body string
		purged    bool
	}{
		{"active-secret", `{"id":"jc_active","code":"fixture-active","join_line":"fixture line"}`, true},
		{"revoked-secret", `{"id":"jc_revoked","code":"fixture-revoked"}`, true},
		{"revoke-receipt", `{"id":"jc_revoked","revoked":true}`, false},
		{"empty-code", `{"id":"jc_active","code":""}`, false},
		{"agent-join", `{"agent":{"id":"mem_agent"},"token":"fixture-token"}`, false},
		{"message", `{"id":"msg_message","body":"unchanged"}`, false},
		{"unrelated-code", `{"id":"jc_unrelated","code":"fixture-other"}`, false},
		{"malformed", `{"id":"jc_active","code":`, false},
	}
	for _, row := range rows {
		status := 201
		if row.key == "revoke-receipt" {
			status = 200
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO idempotency (scope, key, request_hash, status, content_type, body, created_at) VALUES ('fixture', ?, 'hash', ?, 'application/json', ?, ?)", row.key, status, row.body, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	created, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, path, clock.NewFake(created))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, row := range rows {
		response, found, err := st.SavedResponse(ctx, "fixture", row.key)
		if err != nil {
			t.Fatal(err)
		}
		if found == row.purged {
			t.Errorf("cached row %s found=%t, want purged=%t", row.key, found, row.purged)
		}
		if found && string(response.Body) != row.body {
			t.Errorf("unrelated cached row %s changed", row.key)
		}
	}
	if err := st.read(ctx, func(tx *tx) error {
		var count int
		if err := tx.queryRow("SELECT COUNT(*) FROM idempotency").Scan(&count); err != nil {
			return err
		}
		if count != len(rows)-2 {
			t.Errorf("stored row count=%d, want %d after deletion", count, len(rows)-2)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
