package boardtest

import (
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func names(bs []board.Board) []string {
	out := []string{}
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

// An open board is seen by everyone, a private one only by the people on it; the
// private boards someone isn't on are listed oldest first.
func boardVisibilityDecidesWhoSeesIt(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		for _, name := range []string{"zeta", "alpha", "secret"} {
			if _, _, err := newBoard(tx, name); err != nil {
				return err
			}
		}
		if err := tx.InsertHuman(human("hum_blair")); err != nil {
			return err
		}
		return tx.SetBoardVisibility("brd_secret", board.BoardPrivate)
	})
	read(t, st, func(tx board.ReadTx) error {
		b, err := tx.BoardByName("secret")
		if err != nil {
			return err
		}
		if b.Visibility != board.BoardPrivate {
			t.Errorf("visibility after SetBoardVisibility = %q", b.Visibility)
		}
		for humanID, want := range map[string][]string{creatorID: {"alpha", "secret", "zeta"}, "hum_blair": {"alpha", "zeta"}} {
			seen, err := tx.BoardsSeenBy(humanID)
			if err != nil {
				return err
			}
			if got := names(seen); !reflect.DeepEqual(got, want) {
				t.Errorf("BoardsSeenBy(%s) = %v, want %v", humanID, got, want)
			}
		}
		for humanID, want := range map[string][]string{creatorID: {}, "hum_blair": {"secret"}} {
			hidden, err := tx.PrivateBoardsNotOn(humanID)
			if err != nil {
				return err
			}
			if got := names(hidden); !reflect.DeepEqual(got, want) {
				t.Errorf("PrivateBoardsNotOn(%s) = %v, want %v", humanID, got, want)
			}
		}
		return nil
	})
}

// A person who left keeps their row, but isn't on the board: it leaves their boards and
// a private one is hidden from them; their access is kept as set.
func peopleWhoLeftAreNotOnTheBoard(t *testing.T, st board.Store) {
	write(t, st, func(tx board.Tx) error {
		_, m, err := newBoard(tx, "secret")
		if err != nil {
			return err
		}
		if err := tx.SetBoardVisibility("brd_secret", board.BoardPrivate); err != nil {
			return err
		}
		if err := tx.SetMemberAccess(m.ID, "member"); err != nil {
			return err
		}
		return tx.SetMemberStatus(m.ID, board.StatusLeft)
	})
	read(t, st, func(tx board.ReadTx) error {
		m, err := tx.HumanMember("brd_secret", creatorID)
		if err != nil {
			return err
		}
		if m.Status != board.StatusLeft || m.Access != "member" {
			t.Errorf("member after leaving: status %q access %q", m.Status, m.Access)
		}
		for what, list := range map[string]func(string) ([]board.Board, error){
			"BoardsOfHuman": tx.BoardsOfHuman, "BoardsSeenBy": tx.BoardsSeenBy,
		} {
			bs, err := list(creatorID)
			if err != nil {
				return err
			}
			if len(bs) != 0 {
				t.Errorf("%s after leaving = %v", what, names(bs))
			}
		}
		hidden, err := tx.PrivateBoardsNotOn(creatorID)
		if err != nil {
			return err
		}
		if got := names(hidden); !reflect.DeepEqual(got, []string{"secret"}) {
			t.Errorf("PrivateBoardsNotOn after leaving = %v", got)
		}
		return nil
	})
}

func workingJoinCodesSkipRevokedAndExpired(t *testing.T, st board.Store) {
	const now = "2026-10-02T00:00:00.000Z"
	write(t, st, func(tx board.Tx) error {
		b, m, err := newBoard(tx, "docs")
		if err != nil {
			return err
		}
		for _, jc := range []board.JoinCode{
			{ID: "jc_working", ExpiresAt: "2026-10-03T00:00:00.000Z"},
			{ID: "jc_expired", ExpiresAt: now},
			{ID: "jc_revoked", ExpiresAt: "2026-10-03T00:00:00.000Z"},
		} {
			jc.BoardID, jc.CodeDigest, jc.Role, jc.CreatedAt, jc.CreatedBy = b.ID, "digest-"+jc.ID, "member", at, m.ID
			if err := tx.InsertJoinCode(jc); err != nil {
				return err
			}
		}
		return tx.RevokeJoinCode("jc_revoked", at)
	})
	read(t, st, func(tx board.ReadTx) error {
		codes, err := tx.WorkingJoinCodes("brd_docs", now)
		if err != nil {
			return err
		}
		if len(codes) != 1 || codes[0].ID != "jc_working" {
			t.Errorf("WorkingJoinCodes = %+v, want only jc_working", codes)
		}
		return nil
	})
}

func boardCreationDefaultsToMembers(t *testing.T, st board.Store) {
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.BoardCreation()
		if got != board.CreationMembers {
			t.Errorf("BoardCreation before it is set = %q", got)
		}
		return err
	})
	write(t, st, func(tx board.Tx) error { return tx.SetBoardCreation(board.CreationAdmins) })
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.BoardCreation()
		if got != board.CreationAdmins {
			t.Errorf("BoardCreation after it is set = %q", got)
		}
		return err
	})
}
