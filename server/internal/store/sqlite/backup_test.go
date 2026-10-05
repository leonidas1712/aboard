package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/clock"
)

// copyFixture copies the database a local server from before people and access keys
// wrote (schema 10) into a fresh folder and returns its path.
func copyFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../../../e2e/testdata/schema10/aboard.db")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "aboard.db")
	if err := os.WriteFile(path, raw, 0o600); err != nil { //nolint:gosec // a file in the test's own temporary folder
		t.Fatal(err)
	}
	return path
}

func schemaVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Migrating an existing database first copies it, as it was, into the backups folder,
// readable only by its owner; the newest three copies are kept.
func TestMigratingKeepsTheThreeNewestBackups(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC))
	path := copyFixture(t)
	st, err := Open(ctx, path, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "*.db"))
	if len(backups) != 1 {
		t.Fatalf("backups after migrating: %v", backups)
	}
	if info, err := os.Stat(backups[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup: %v %v", info, err)
	}
	if v := schemaVersion(t, backups[0]); v != 10 {
		t.Fatalf("the backup is at schema %d, want 10, as before the migration", v)
	}
	if v := schemaVersion(t, path); v < 11 {
		t.Fatalf("the database is at schema %d after migrating", v)
	}

	for range 4 {
		clk.Advance(time.Second)
		if err := st.backup(ctx, 11); err != nil {
			t.Fatal(err)
		}
	}
	kept, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "*.db"))
	if len(kept) != keptBackups {
		t.Fatalf("kept %d backups, want %d: %v", len(kept), keptBackups, kept)
	}
	for _, k := range kept {
		if k == backups[0] {
			t.Fatalf("the oldest backup was kept: %v", kept)
		}
	}
}

// After the upgrade the first person is the admin, their login is their first key, and
// every agent token and browser login is tied to that key, so revoking it later ends
// them too.
func TestUpgradeTiesAgentsAndBrowsersToTheFirstKey(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, copyFixture(t), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var role, keyID string
	var untiedAgents, untiedBrowsers, keys int
	err = st.read(ctx, func(x *tx) error {
		if err := x.queryRow("SELECT h.role, k.id FROM humans h JOIN access_keys k ON k.human_id = h.id").Scan(&role, &keyID); err != nil {
			return err
		}
		if err := x.queryRow("SELECT count(*) FROM access_keys").Scan(&keys); err != nil {
			return err
		}
		if err := x.queryRow("SELECT count(*) FROM members WHERE kind = 'agent' AND key_id IS NOT ?", keyID).Scan(&untiedAgents); err != nil {
			return err
		}
		return x.queryRow("SELECT count(*) FROM browser_logins WHERE key_id IS NOT ?", keyID).Scan(&untiedBrowsers)
	})
	if err != nil {
		t.Fatal(err)
	}
	if role != "admin" || keys != 1 || untiedAgents != 0 || untiedBrowsers != 0 {
		t.Fatalf("after the upgrade: role %s, %d keys, %d agents and %d browser logins not tied to the key", role, keys, untiedAgents, untiedBrowsers)
	}
}

// A new database has nothing to back up.
func TestANewDatabaseIsNotBackedUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aboard.db")
	st, err := Open(context.Background(), path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "backups")); !os.IsNotExist(err) {
		t.Fatalf("a new database made a backups folder: %v", err)
	}
}

// Upgrading gives keys that came from invites before keys expired the expiry such keys
// have now, 90 days without use, and leaves the first person's own key as it was.
func TestUpgradeGivesInvitedPeoplesKeysAnIdleExpiry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	st, err := Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	err = st.write(ctx, func(x *tx) error {
		for _, q := range []string{
			"INSERT INTO humans (id, name, role, created_at) VALUES ('hum_a', 'alex', 'admin', '2026-10-01T16:00:00.000Z'), ('hum_m', 'maya', 'member', '2026-10-01T16:00:00.000Z')",
			"INSERT INTO access_keys (id, human_id, name, digest, created_at) VALUES ('key_a', 'hum_a', 'laptop', 'd-a', '2026-10-01T16:00:00.000Z'), ('key_m', 'hum_m', 'laptop', 'd-m', '2026-10-01T16:00:00.000Z')",
			"PRAGMA user_version = 12",
		} {
			if err := x.exec(q); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if st, err = Open(ctx, path, clock.Real{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	expires := map[string]*string{}
	idle := map[string]*int64{}
	err = st.read(ctx, func(x *tx) error {
		for _, id := range []string{"key_a", "key_m"} {
			var e *string
			var i *int64
			if err := x.queryRow("SELECT expires_at, idle_seconds FROM access_keys WHERE id = ?", id).Scan(&e, &i); err != nil {
				return err
			}
			expires[id], idle[id] = e, i
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if expires["key_a"] != nil || idle["key_a"] != nil {
		t.Fatalf("the admin's own key got an expiry: %v %v", *expires["key_a"], idle["key_a"])
	}
	if idle["key_m"] == nil || *idle["key_m"] != 90*24*3600 || expires["key_m"] == nil {
		t.Fatalf("maya's key after the upgrade: expires %v, idle %v", expires["key_m"], idle["key_m"])
	}
	at, err := time.Parse("2006-01-02T15:04:05.000Z", *expires["key_m"])
	if err != nil || time.Until(at) < 89*24*time.Hour || time.Until(at) > 91*24*time.Hour {
		t.Fatalf("maya's key expires at %v (%v)", *expires["key_m"], err)
	}
}
