package boardtest

import (
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func teamworkGatesAndReceipts(t *testing.T, st board.Store) {
	var b board.Board
	receipt := board.CreationReceipt{DelegationID: "dlg_receipt", Key: "operation", RequestHash: "raw-request-hash", CreatedAt: at}
	write(t, st, func(tx board.Tx) error {
		var err error
		b, _, err = newBoard(tx, "teamwork")
		if err != nil {
			return err
		}
		allowed, err := tx.AgentsAddPeople()
		if err != nil {
			return err
		}
		if !allowed {
			t.Error("server gate must default to true")
		}
		if err := tx.SetAgentsAddPeople(false); err != nil {
			return err
		}
		if err := tx.SetBoardAgentsAddPeople(b.ID, true); err != nil {
			return err
		}
		if err := tx.InsertAccessKey(board.AccessKey{ID: "key_receipt", HumanID: creatorID, Name: "machine", Digest: "receipt-key", CreatedAt: at}); err != nil {
			return err
		}
		if err := tx.InsertDelegation(board.Delegation{ID: receipt.DelegationID, KeyID: "key_receipt", HumanID: creatorID, Name: "machine", Digest: "receipt-delegation", CreatedAt: at}); err != nil {
			return err
		}
		receipt.Joined = board.Joined{View: board.View{Board: b}, Token: "secret-for-replay"}
		return tx.SaveDelegatedCreation(receipt)
	})
	read(t, st, func(tx board.ReadTx) error {
		allowed, err := tx.AgentsAddPeople()
		if err != nil {
			return err
		}
		if allowed {
			t.Error("server gate was not disabled")
		}
		current, err := tx.BoardByID(b.ID)
		if err != nil {
			return err
		}
		if !current.AgentsAddPeople {
			t.Error("board gate was not enabled")
		}
		got, err := tx.DelegatedCreation(receipt.DelegationID, receipt.Key)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, receipt) {
			t.Errorf("receipt differs: %+v / %+v", got, receipt)
		}
		return nil
	})
}
