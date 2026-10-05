package boardtest

import (
	"errors"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// delegationsEndByKeyAndName checks that a delegation reads back by digest and id, and
// that ending a key's delegations under a name ends only those, and counts them per key.
func delegationsEndByKeyAndName(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		if err := tx.InsertHuman(human("hum_maya")); err != nil {
			return err
		}
		k := board.AccessKey{ID: "key_1", HumanID: "hum_maya", Name: "laptop", Digest: "kd1", CreatedAt: at}
		if err := tx.InsertAccessKey(k); err != nil {
			return err
		}
		for _, d := range []board.Delegation{
			{ID: "dlg_1", KeyID: "key_1", HumanID: "hum_maya", Name: "laptop", Digest: "d1", CreatedAt: at},
			{ID: "dlg_2", KeyID: "key_1", HumanID: "hum_maya", Name: "desktop", Digest: "d2", CreatedAt: at},
		} {
			if err := tx.InsertDelegation(d); err != nil {
				return err
			}
		}
		return tx.EndDelegations("key_1", "laptop", "2026-10-01T17:00:00.000Z")
	})
	read(t, st, func(tx board.ReadTx) error {
		d, err := tx.DelegationByDigest("d1")
		if err != nil || d.ID != "dlg_1" || d.EndedAt == nil || *d.EndedAt != "2026-10-01T17:00:00.000Z" {
			t.Errorf("ended delegation: %+v %v", d, err)
		}
		d, err = tx.DelegationByID("dlg_2")
		if err != nil || d.Digest != "d2" || d.EndedAt != nil || d.KeyID != "key_1" || d.HumanID != "hum_maya" || d.Name != "desktop" {
			t.Errorf("working delegation: %+v %v", d, err)
		}
		if _, err := tx.DelegationByDigest("nope"); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("a missing delegation: %v", err)
		}
		keys, err := tx.KeysOf("hum_maya", at)
		if err != nil || len(keys) != 1 || keys[0].Delegations != 1 {
			t.Errorf("keys: %+v %v", keys, err)
		}
		return nil
	})
}

// seatsAreFoundBySessionAndPerson checks that a seat is found by its board, person and
// session together, newest first, never another person's with the same session; that
// its token can be replaced; and that a removed agent keeps when and by whom.
func seatsAreFoundBySessionAndPerson(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "seats")
		if err != nil {
			return err
		}
		if err := tx.InsertHuman(human("hum_sam")); err != nil {
			return err
		}
		if err := tx.InsertAccessKey(board.AccessKey{ID: "key_x", HumanID: creatorID, Name: "laptop", Digest: "kx", CreatedAt: at}); err != nil {
			return err
		}
		old := agent(b, creatorID, "claude", "member")
		old.Session = ptr("claude-code:1")
		newer := agent(b, creatorID, "claude-2", "member")
		newer.Session, newer.JoinedAt = ptr("claude-code:1"), "2026-10-01T16:00:01.000Z"
		sams := agent(b, "hum_sam", "claude-3", "member")
		sams.Session, sams.JoinedAt = ptr("claude-code:1"), "2026-10-01T16:00:02.000Z"
		for _, m := range []board.Member{old, newer, sams} {
			if err := tx.InsertMember(m); err != nil {
				return err
			}
		}
		if err := tx.RemoveAgent(old.ID, "2026-10-01T16:30:00.000Z", board.RemovedByOwner); err != nil {
			return err
		}
		return tx.SetAgentToken(newer.ID, "rotated", "key_x")
	})
	read(t, st, func(tx board.ReadTx) error {
		m, err := tx.SeatForSession("brd_seats", creatorID, "claude-code:1")
		if err != nil || m.Name != "claude-2" || m.Session == nil || *m.Session != "claude-code:1" {
			t.Errorf("alex's seat: %+v %v", m, err)
		}
		if m.TokenDigest == nil || *m.TokenDigest != "rotated" || m.KeyID == nil || *m.KeyID != "key_x" {
			t.Errorf("the replaced token: %+v", m)
		}
		if _, err := tx.MemberByTokenDigest("digest-seats-claude-2"); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("the earlier token still finds the seat: %v", err)
		}
		m, err = tx.SeatForSession("brd_seats", "hum_sam", "claude-code:1")
		if err != nil || m.Name != "claude-3" {
			t.Errorf("sam's seat: %+v %v", m, err)
		}
		if _, err := tx.SeatForSession("brd_seats", "hum_sam", "claude-code:2"); !errors.Is(err, board.ErrNotFound) {
			t.Errorf("another session: %v", err)
		}
		removed, err := tx.MemberByID("mem_seats_claude")
		if err != nil || removed.Status != board.StatusRemoved || removed.RemovedAt == nil || removed.RemovedBy == nil || *removed.RemovedBy != board.RemovedByOwner {
			t.Errorf("the removed agent: %+v %v", removed, err)
		}
		return nil
	})
}
