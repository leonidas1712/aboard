package board_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func TestUnreadCountsFollowReadableWakingMentions(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{Policy: &rules.PolicyChange{Visibility: rules.VisibilityOpen}}); err != nil {
		t.Fatal(err)
	}
	posted, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{To: []string{"@maya"}, Body: "Please check this @" + w.samAgent.Agent.Name})
	if err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []string{rules.VisibilityOpen, rules.VisibilityAddressed} {
		if _, err := w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{Policy: &rules.PolicyChange{Visibility: visibility}}); err != nil {
			t.Fatal(err)
		}
		inbox, _, err := w.svc.Inbox(ctx, w.samAgent, 0, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		v, err := w.svc.GetBoard(ctx, w.samAgent, w.board)
		if err != nil {
			t.Fatal(err)
		}
		if v.Position == nil || v.Position.Unread != int64(len(inbox.Messages)) {
			t.Fatalf("%s count differs from inbox: position=%+v, inbox=%+v", visibility, v.Position, inbox)
		}
		if visibility == rules.VisibilityOpen && (len(inbox.Messages) != 1 || inbox.Messages[0].Seq != posted.Seq) {
			t.Fatalf("open board omitted the waking mention: %+v", inbox)
		}
		if visibility == rules.VisibilityAddressed && len(inbox.Messages) != 0 {
			t.Fatalf("addressed policy exposed mention-only content: %+v", inbox)
		}
	}
	// Mentions ask for attention; only `to` freezes receipt recipients.
	receipts, err := w.svc.Receipts(ctx, w.maya, w.board, posted.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if !receipts.Available || len(receipts.Recipients) != 0 {
		t.Fatalf("a mention became a receipt recipient: %+v", receipts)
	}
}

func TestANewGuestStartsWithoutUnreadHistory(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	if _, err := w.svc.PostMessage(ctx, w.maya, w.board, board.NewMessage{Body: "before the guest was invited"}); err != nil {
		t.Fatal(err)
	}
	joined, err := w.svc.GuestJoin(ctx, board.GuestJoinInput{Code: w.guestCode(ctx, t, w.maya, "kim"), KeyName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	guest := w.auth(ctx, t, joined.KeyToken)
	v, err := w.svc.GetBoard(ctx, guest, w.board)
	if err != nil {
		t.Fatal(err)
	}
	if v.Position == nil || v.Position.Unread != 0 {
		t.Fatalf("first guest membership counted pre-join history as unread: %+v", v.Position)
	}
}
