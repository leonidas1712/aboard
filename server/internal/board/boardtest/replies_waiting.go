package boardtest

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func repliesWaitingUseRecordedRecipientsAndLatestRequests(t *testing.T, st board.Store) {
	c := newConversation(t, st)
	insert := func(m board.Message) {
		t.Helper()
		m.BoardID, m.At = "brd_docs", at
		write(t, st, func(tx board.Tx) error { return tx.InsertMessage(m) })
	}
	request := func(id string, seq int64, sender board.Member, root *string) board.Message {
		m := board.Message{ID: id, Seq: seq, SenderID: sender.ID, To: []string{"@alex"}, ExpectsReply: true, Recipients: []string{c.alex.ID}}
		if root != nil {
			m.ReplyTo, m.ThreadRoot = root, root
		}
		return m
	}
	check := func(reader board.Member, count int64, id string) {
		t.Helper()
		read(t, st, func(tx board.ReadTx) error {
			msgs, got, err := tx.RepliesWaiting(reader, 1)
			if err != nil {
				return err
			}
			if got != count {
				t.Errorf("count=%d, want %d", got, count)
			}
			if id == "" {
				if len(msgs) != 0 {
					t.Errorf("unexpected requests: %+v", msgs)
				}
			} else if len(msgs) != 1 || msgs[0].ID != id {
				t.Errorf("limited messages=%+v, want %s", msgs, id)
			}
			return nil
		})
	}
	root := "request_06"
	insert(request(root, 6, c.writer, nil))
	legacy := request("legacy", 7, c.writer, nil)
	legacy.Recipients = nil
	insert(legacy)
	insert(request("request_08", 8, c.writer, &root))
	insert(board.Message{ID: "peer_09", Seq: 9, SenderID: c.reviewer.ID, To: []string{"all"}, ReplyTo: ptr("request_08"), ThreadRoot: &root})
	insert(request("request_10", 10, c.other, &root))
	insert(board.Message{ID: "info_11", Seq: 11, SenderID: c.writer.ID, To: []string{"@alex"}, Recipients: []string{c.alex.ID}, ReplyTo: &root, ThreadRoot: &root})
	check(c.alex, 2, "request_10")
	replacement := c.alex
	replacement.ID = "new-membership-same-name"
	check(replacement, 0, "")
	insert(board.Message{ID: "person_12", Seq: 12, SenderID: c.alex.ID, To: []string{"all"}, ReplyTo: ptr("peer_09"), ThreadRoot: &root})
	check(c.alex, 0, "")
	insert(request("request_13", 13, c.writer, &root))
	check(c.alex, 1, "request_13")
}
