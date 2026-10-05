package board_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestRejoiningDoesNotAcknowledgeUnreadMessages(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	msg, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{To: []string{"@sam"}, Body: "never read"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := w.svc.Receipts(ctx, w.maya, w.board, msg.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Recipients) != 1 || before.Recipients[0].State != board.ReceiptPending {
		t.Fatalf("before: %+v", before.Recipients)
	}
	if _, err = w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.AddPerson(ctx, w.maya, w.board, "sam"); err != nil {
		t.Fatal(err)
	}
	after, err := w.svc.Receipts(ctx, w.maya, w.board, msg.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Recipients) != 1 || after.Recipients[0].State != board.ReceiptPending {
		t.Fatalf("rejoining changed the pending receipt: %+v", after.Recipients)
	}
}
