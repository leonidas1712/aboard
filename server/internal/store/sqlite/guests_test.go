package sqlite

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

// A team server from before guests and removing people (schema 14) upgrades with nothing
// lost: its people keep their ids, handles, display names and roles, and are still on the
// server; every key, agent, invite and browser login still points at its person; and
// every join code it had becomes a pairing code that still works. A backup of the old
// database is kept.
func TestUpgradeFromSchema14KeepsPeopleAndMakesCodesPairingCodes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	db := databaseAt(t, path, 14)
	const at = "2026-10-01T16:00:00.000Z"
	for _, q := range []string{
		"INSERT INTO humans (id, name, display_name, role, created_at) VALUES ('hum_a', 'alex', NULL, 'admin', '" + at + "'), ('hum_m', 'maya', 'Maya Chen', 'member', '" + at + "')",
		"INSERT INTO access_keys (id, human_id, name, digest, created_at) VALUES ('key_a', 'hum_a', 'laptop', 'd-a', '" + at + "'), ('key_m', 'hum_m', 'laptop', 'd-m', '" + at + "')",
		"INSERT INTO server_invites (id, digest, created_by, created_at, expires_at) VALUES ('inv_1', 'd-inv', 'hum_a', '" + at + "', '2026-10-08T16:00:00.000Z')",
		"INSERT INTO browser_logins (token_digest, human_id, key_id, created_at, expires_at) VALUES ('d-b', 'hum_m', 'key_m', '" + at + "', '2099-01-01T00:00:00.000Z')",
		"INSERT INTO boards (id, name, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by) VALUES ('brd_1', 'docs', '', '{}', '{}', 0, 'h', '" + at + "', 'mem_a')",
		"INSERT INTO members (id, board_id, name, kind, human_id, status, joined_at, access) VALUES ('mem_a', 'brd_1', 'alex', 'human', 'hum_a', 'active', '" + at + "', 'admin'), ('mem_m', 'brd_1', 'maya', 'human', 'hum_m', 'active', '" + at + "', 'member')",
		"INSERT INTO members (id, board_id, name, kind, role, human_id, owner, token_digest, status, joined_at, key_id) VALUES ('mem_c', 'brd_1', 'claude', 'agent', 'member', 'hum_m', 'maya', 'd-c', 'active', '" + at + "', 'key_m')",
		"INSERT INTO join_codes (id, board_id, code_digest, role, expires_at, created_at, created_by) VALUES ('jc_1', 'brd_1', 'd-jc', 'member', '2099-01-01T00:00:00.000Z', '" + at + "', 'mem_c')",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()
	st, err := Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	if v, latest := schemaVersion(t, path), migrationNumber(names[len(names)-1]); v != latest {
		t.Fatalf("schema after the upgrade = %d, want %d", v, latest)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(path), "backups", "aboard-*-schema-14.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups of schema 14: %v %v", backups, err)
	}
	err = st.Read(ctx, func(tx board.ReadTx) error {
		people, err := tx.PeopleOnServer()
		if err != nil {
			return err
		}
		if len(people) != 2 || people[0].Name != "alex" || people[0].Role != board.ServerAdmin ||
			people[1].Name != "maya" || people[1].Role != board.ServerMember || people[1].DisplayName == nil || *people[1].DisplayName != "Maya Chen" ||
			people[0].RemovedAt != nil || people[1].RemovedAt != nil {
			t.Errorf("people after the upgrade: %+v", people)
		}
		if k, err := tx.AccessKeyByDigest("d-m"); err != nil || k.HumanID != "hum_m" {
			t.Errorf("maya's key after the upgrade: %+v %v", k, err)
		}
		if l, err := tx.BrowserLoginByDigest("d-b"); err != nil || l.HumanID != "hum_m" || l.KeyID != "key_m" {
			t.Errorf("maya's browser login after the upgrade: %+v %v", l, err)
		}
		if inv, err := tx.ServerInviteByDigest("d-inv"); err != nil || inv.CreatedBy != "hum_a" {
			t.Errorf("the invite after the upgrade: %+v %v", inv, err)
		}
		agent, err := tx.MemberByTokenDigest("d-c")
		if err != nil || agent.HumanID != "hum_m" || agent.PersonRole != board.ServerMember {
			t.Errorf("maya's agent after the upgrade: %+v %v", agent, err)
		}
		jc, err := tx.JoinCodeByDigest("d-jc")
		if err != nil || jc.Kind != board.CodePairing || jc.Guest != nil || jc.UsedAt != nil {
			t.Errorf("the join code after the upgrade: %+v %v", jc, err)
		}
		codes, err := tx.WorkingJoinCodes("brd_1", at)
		if err != nil || len(codes) != 1 {
			t.Errorf("working join codes after the upgrade: %+v %v", codes, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A handle is still unique among the people on the server.
	err = st.Write(ctx, func(tx board.Tx) error {
		return tx.InsertHuman(board.Human{ID: "hum_x", Name: "maya", Role: board.ServerMember, CreatedAt: at})
	})
	if err == nil {
		t.Fatal("a second maya was stored after the upgrade")
	}
}
