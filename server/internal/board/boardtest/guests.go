package boardtest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// A person removed from the server keeps their row, found by id with who removed them
// and when, but no longer by handle; the handle is free for a new person at once, and
// the list of the server's people leaves them out.
func removedPeopleFreeTheirHandles(t *testing.T, st board.Store) {
	admin := human("hum_alex")
	admin.Role = board.ServerAdmin
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(admin); err != nil {
			return err
		}
		if err := tx.InsertHuman(board.Human{ID: "hum_maya", Name: "maya", Role: board.ServerMember, CreatedAt: at}); err != nil {
			return err
		}
		return tx.RemoveHuman("hum_maya", at, "hum_alex")
	})
	write(t, st, func(tx board.Tx) error {
		// Removing again keeps the first time.
		return tx.RemoveHuman("hum_maya", "2026-10-02T16:00:00.000Z", "hum_alex")
	})
	read(t, st, func(tx board.ReadTx) error {
		if _, err := tx.HumanByName("maya"); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("HumanByName of a removed person = %v, want ErrNotFound", err)
		}
		got, err := tx.HumanByID("hum_maya")
		if err != nil {
			return err
		}
		if got.RemovedAt == nil || *got.RemovedAt != at || got.RemovedBy == nil || *got.RemovedBy != "hum_alex" {
			t.Errorf("a removed person = %+v, want removed at %s by hum_alex", got, at)
		}
		return nil
	})
	newMaya := board.Human{ID: "hum_maya2", Name: "maya", Role: board.ServerMember, CreatedAt: at}
	write(t, st, func(tx board.Tx) error { return tx.InsertHuman(newMaya) })
	newMaya.MidturnPolicy = board.MidturnMyAgents
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(board.Human{ID: "hum_maya3", Name: "maya", Role: board.ServerMember, CreatedAt: at}); err == nil {
			return errors.New("a second person took the handle of someone still on the server")
		}
		return nil
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.HumanByName("maya")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, newMaya) {
			t.Errorf("HumanByName after the handle was reused = %+v, want %+v", got, newMaya)
		}
		people, err := tx.PeopleOnServer()
		if err != nil {
			return err
		}
		if len(people) != 2 || people[0].ID != "hum_alex" || people[1].ID != "hum_maya2" {
			t.Errorf("PeopleOnServer = %+v, want alex then the new maya", people)
		}
		if n, err := tx.HumanCount(); err != nil || n != 3 {
			t.Errorf("HumanCount = %d, %v; want 3, removed people included", n, err)
		}
		return nil
	})
}

// AdminCount counts the admins still on the server, and SetHumanRole changes a role.
func serverRolesAndAdminsCounted(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		for _, h := range []board.Human{
			{ID: "hum_a", Name: "a", Role: board.ServerAdmin, CreatedAt: at},
			{ID: "hum_b", Name: "b", Role: board.ServerAdmin, CreatedAt: at},
			{ID: "hum_c", Name: "c", Role: board.ServerMember, CreatedAt: at},
			{ID: "hum_g", Name: "g", Role: board.ServerGuest, CreatedAt: at},
		} {
			if err := tx.InsertHuman(h); err != nil {
				return err
			}
		}
		return nil
	})
	count := func(want int) {
		t.Helper()
		read(t, st, func(tx board.ReadTx) error {
			if n, err := tx.AdminCount(); err != nil || n != want {
				t.Errorf("AdminCount = %d, %v; want %d", n, err, want)
			}
			return nil
		})
	}
	count(2)
	write(t, st, func(tx board.Tx) error { return tx.SetHumanRole("hum_c", board.ServerAdmin) })
	count(3)
	write(t, st, func(tx board.Tx) error { return tx.SetHumanRole("hum_a", board.ServerMember) })
	write(t, st, func(tx board.Tx) error { return tx.RemoveHuman("hum_b", at, "hum_c") })
	count(1)
	read(t, st, func(tx board.ReadTx) error {
		g, err := tx.HumanByName("g")
		if err == nil && g.Role != board.ServerGuest {
			t.Errorf("a guest's role = %q", g.Role)
		}
		return err
	})
}

// A guest code keeps its kind and guest, is used once, and stops being a working code
// once used.
func guestCodesAreUsedOnce(t *testing.T, st board.Store) {
	const now = "2026-10-01T17:00:00.000Z"
	var want board.JoinCode
	write(t, st, func(tx board.Tx) error {
		b, m, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		want = joinCode(b, m)
		want.ID, want.CodeDigest, want.Kind, want.Guest = "jc_guest", "digest-guest", board.CodeGuest, ptr("sam")
		if err := tx.InsertJoinCode(want); err != nil {
			return err
		}
		return tx.InsertMember(agent(b, creatorID, "claude", "member"))
	})
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.JoinCodeByDigest("digest-guest")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("JoinCodeByDigest = %+v, want %+v", got, want)
		}
		return nil
	})
	for i, wantUsed := range []bool{true, false} {
		write(t, st, func(tx board.Tx) error {
			used, err := tx.UseJoinCode("jc_guest", now, "mem_docs_claude")
			if err == nil && used != wantUsed {
				t.Errorf("use %d: UseJoinCode = %v, want %v", i+1, used, wantUsed)
			}
			return err
		})
	}
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.JoinCodeByID("jc_guest")
		if err != nil {
			return err
		}
		if got.UsedAt == nil || *got.UsedAt != now || got.UsedBy == nil || *got.UsedBy != "mem_docs_claude" {
			t.Errorf("a used guest code = %+v", got)
		}
		codes, err := tx.WorkingJoinCodes("brd_docs", now)
		if err != nil {
			return err
		}
		if len(codes) != 0 {
			t.Errorf("WorkingJoinCodes after the guest code was used = %+v, want none", codes)
		}
		return nil
	})
}

// Every member comes back with the server role of its person, or of its owner for an
// agent, as it is now.
func membersCarryTheirPersonsRole(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		if err := tx.InsertHuman(board.Human{ID: "hum_sam", Name: "sam", Role: board.ServerGuest, CreatedAt: at}); err != nil {
			return err
		}
		if err := tx.InsertMember(board.Member{
			ID: "mem_docs_sam", BoardID: b.ID, Name: "sam", Kind: "human", HumanID: "hum_sam",
			Access: rules.AccessMember, Status: board.StatusActive, JoinedAt: at,
		}); err != nil {
			return err
		}
		return tx.InsertMember(agent(b, "hum_sam", "claude", "member"))
	})
	write(t, st, func(tx board.Tx) error { return tx.SetHumanRole(creatorID, board.ServerAdmin) })
	read(t, st, func(tx board.ReadTx) error {
		members, err := tx.Members("brd_docs")
		if err != nil {
			return err
		}
		want := map[string]string{"mem_docs": board.ServerAdmin, "mem_docs_sam": board.ServerGuest, "mem_docs_claude": board.ServerGuest}
		for _, m := range members {
			if m.PersonRole != want[m.ID] {
				t.Errorf("%s's PersonRole = %q, want %q", m.ID, m.PersonRole, want[m.ID])
			}
		}
		agent, err := tx.MemberByTokenDigest("digest-docs-claude")
		if err == nil && agent.PersonRole != board.ServerGuest {
			t.Errorf("MemberByTokenDigest's PersonRole = %q, want guest", agent.PersonRole)
		}
		return err
	})
}
