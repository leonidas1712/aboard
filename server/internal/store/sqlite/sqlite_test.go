package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/board/boardtest"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

func TestSQLiteIsABoardStore(t *testing.T) {
	boardtest.Run(t, func(t *testing.T) board.Store {
		clk := clock.NewFake(time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC))
		st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "aboard.db"), clk)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	})
}

// A database written by a newer aboard, with migrations this one doesn't know, is
// refused rather than misread.
func TestDatabaseFromANewerAboardIsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 9999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := sqlite.Open(ctx, path, clock.Real{}); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Fatalf("opening a newer database: got %v, want ErrNewerSchema", err)
	}
}

// People on boards created before access levels existed get them on upgrade: each
// board's creator is its admin and every other person a member; agents get none.
func TestUpgradeGivesExistingPeopleAccessLevels(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	initial, err := os.ReadFile(filepath.Join("migrations", "0001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		string(initial),
		"PRAGMA user_version = 1",
		`INSERT INTO humans VALUES ('hum_alex', 'alex', 'd1', 'x'), ('hum_sam', 'sam', 'd2', 'x')`,
		`INSERT INTO boards VALUES ('brd_a', 'docs', NULL, '', '{}', '{}', 0, 'h', 'x', 'mem_alex')`,
		`INSERT INTO members (id, board_id, name, kind, role, human_id, owner, status, joined_at) VALUES
			('mem_alex', 'brd_a', 'alex', 'human', NULL, 'hum_alex', NULL, 'active', 'x'),
			('mem_sam', 'brd_a', 'sam', 'human', NULL, 'hum_sam', NULL, 'active', 'x'),
			('mem_writer', 'brd_a', 'writer', 'agent', 'writer', 'hum_sam', 'sam', 'active', 'x')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	got := map[string]string{}
	err = st.Read(ctx, func(tx board.ReadTx) error {
		ms, err := tx.Members("brd_a")
		for _, m := range ms {
			got[m.Name] = m.Access
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"alex": "admin", "sam": "member", "writer": ""}; !maps.Equal(got, want) {
		t.Fatalf("access after upgrade %v, want %v", got, want)
	}
}

// Replies posted before threads were kept get their thread on upgrade: a reply to a
// reply joins the thread of the first message.
func TestUpgradeFindsEachReplysThread(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	initial, err := os.ReadFile(filepath.Join("migrations", "0001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		string(initial),
		"PRAGMA user_version = 1",
		`INSERT INTO humans VALUES ('hum_alex', 'alex', 'd1', 'x')`,
		`INSERT INTO boards VALUES ('brd_a', 'docs', NULL, '', '{}', '{}', 0, 'h', 'x', 'mem_alex')`,
		`INSERT INTO members (id, board_id, name, kind, role, human_id, owner, status, joined_at) VALUES
			('mem_alex', 'brd_a', 'alex', 'human', NULL, 'hum_alex', NULL, 'active', 'x')`,
		`INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, urgent, expects_reply, redactions_json) VALUES
			('msg_1', 'brd_a', 1, 'x', 'mem_alex', '["all"]', 'one', NULL, 0, 0, '[]'),
			('msg_2', 'brd_a', 2, 'x', 'mem_alex', '["all"]', 'two', 'msg_1', 0, 0, '[]'),
			('msg_3', 'brd_a', 3, 'x', 'mem_alex', '["all"]', 'three', 'msg_2', 0, 0, '[]'),
			('msg_4', 'brd_a', 4, 'x', 'mem_alex', '["all"]', 'four', NULL, 0, 0, '[]')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	got := map[string]string{}
	err = st.Read(ctx, func(tx board.ReadTx) error {
		ms, err := tx.MessagesBySeq("brd_a", []int64{1, 2, 3, 4})
		for _, m := range ms {
			if m.ThreadRoot != nil {
				got[m.ID] = *m.ThreadRoot
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"msg_2": "msg_1", "msg_3": "msg_1"}; !maps.Equal(got, want) {
		t.Fatalf("threads after upgrade %v, want %v", got, want)
	}
}

// A database at schema 13, the last before boards were open or private, comes up with
// every board open, its creator still its owner (admin access) and its people still on it.
func TestUpgradeFromSchema13MakesBoardsOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 13; n++ {
		names, err := filepath.Glob(filepath.Join("migrations", fmt.Sprintf("%04d_*.sql", n)))
		if err != nil || len(names) != 1 {
			t.Fatalf("migration %d: %v %v", n, names, err)
		}
		body, err := os.ReadFile(names[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", names[0], err)
		}
	}
	for _, q := range []string{
		"PRAGMA user_version = 13",
		`INSERT INTO humans (id, name, role, created_at) VALUES ('hum_alex', 'alex', 'admin', 'x'), ('hum_sam', 'sam', 'member', 'x')`,
		`INSERT INTO boards (id, name, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by) VALUES
			('brd_a', 'docs', '', '{}', '{}', 0, 'h', 'x', 'mem_alex')`,
		`INSERT INTO members (id, board_id, name, kind, role, human_id, owner, access, status, joined_at) VALUES
			('mem_alex', 'brd_a', 'alex', 'human', NULL, 'hum_alex', NULL, 'admin', 'active', 'x'),
			('mem_sam', 'brd_a', 'sam', 'human', NULL, 'hum_sam', NULL, 'member', 'active', 'x')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	err = st.Read(ctx, func(tx board.ReadTx) error {
		b, err := tx.BoardByName("docs")
		if err != nil {
			return err
		}
		if b.Visibility != board.BoardOpen {
			t.Errorf("visibility after upgrade: %q", b.Visibility)
		}
		alex, err := tx.HumanMember("brd_a", "hum_alex")
		if err != nil {
			return err
		}
		if alex.Access != "admin" || alex.Status != board.StatusActive {
			t.Errorf("creator after upgrade: access %q status %q", alex.Access, alex.Status)
		}
		for _, h := range []string{"hum_alex", "hum_sam"} {
			on, err := tx.BoardsOfHuman(h)
			if err != nil {
				return err
			}
			if len(on) != 1 {
				t.Errorf("%s's boards after upgrade: %d", h, len(on))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A database from before read positions were kept for people, or recipients for
// messages, comes up with each person at their board's head, so nothing they saw counts
// as unread, and each message to someone with the members it was addressed to then: those
// named, and those with the role who had joined by then, never the sender.
func TestUpgradeStartsPeopleAtTheHeadAndFixesRecipients(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aboard.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 15; n++ {
		names, err := filepath.Glob(filepath.Join("migrations", fmt.Sprintf("%04d_*.sql", n)))
		if err != nil || len(names) != 1 {
			t.Fatalf("migration %d: %v %v", n, names, err)
		}
		body, err := os.ReadFile(names[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", names[0], err)
		}
	}
	for _, q := range []string{
		"PRAGMA user_version = 15",
		`INSERT INTO humans (id, name, role, created_at) VALUES ('hum_alex', 'alex', 'admin', 'x'), ('hum_sam', 'sam', 'member', 'x')`,
		`INSERT INTO boards (id, name, charter, roles_json, policy_json, head_seq, head_hash, created_at, created_by) VALUES
			('brd_a', 'docs', '', '{}', '{}', 9, 'h', 'x', 'mem_alex')`,
		`INSERT INTO members (id, board_id, name, kind, role, human_id, owner, access, status, joined_at) VALUES
			('mem_alex', 'brd_a', 'alex', 'human', NULL, 'hum_alex', NULL, 'admin', 'active', '2026-10-01T10:00:00.000Z'),
			('mem_sam', 'brd_a', 'sam', 'human', NULL, 'hum_sam', NULL, 'member', 'active', '2026-10-01T10:00:00.000Z'),
			('mem_writer', 'brd_a', 'writer', 'agent', 'writer', 'hum_alex', 'alex', NULL, 'active', '2026-10-01T10:00:00.000Z'),
			('mem_late', 'brd_a', 'late', 'agent', 'writer', 'hum_sam', 'sam', NULL, 'active', '2026-10-01T12:00:00.000Z')`,
		`INSERT INTO messages (id, board_id, seq, at, sender_id, to_json, body, reply_to, urgent, expects_reply, redactions_json) VALUES
			('msg_6', 'brd_a', 6, '2026-10-01T11:00:00.000Z', 'mem_alex', '["all"]', 'everyone', NULL, 0, 0, '[]'),
			('msg_7', 'brd_a', 7, '2026-10-01T11:00:00.000Z', 'mem_alex', '["@sam"]', 'sam', NULL, 0, 0, '[]'),
			('msg_8', 'brd_a', 8, '2026-10-01T11:00:00.000Z', 'mem_alex', '["role:writer"]', 'writers', NULL, 0, 0, '[]'),
			('msg_9', 'brd_a', 9, '2026-10-01T11:30:00.000Z', 'mem_writer', '["role:writer"]', 'my role', NULL, 0, 0, '[]')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	st, err := sqlite.Open(ctx, path, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	err = st.Read(ctx, func(tx board.ReadTx) error {
		for _, h := range []string{"hum_alex", "hum_sam"} {
			m, err := tx.HumanMember("brd_a", h)
			if err != nil {
				return err
			}
			if m.Cursor != 9 {
				t.Errorf("%s's read position after upgrade: %d, want 9", h, m.Cursor)
			}
		}
		ms, err := tx.MessagesBySeq("brd_a", []int64{6, 7, 8, 9})
		if err != nil {
			return err
		}
		want := map[int64][]string{6: nil, 7: {"mem_sam"}, 8: {"mem_writer"}, 9: {}}
		for seq, w := range want {
			if got := ms[seq].Recipients; !reflect.DeepEqual(got, w) {
				t.Errorf("recipients of #%d after upgrade: %#v, want %#v", seq, got, w)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
