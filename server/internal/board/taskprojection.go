package board

import "github.com/leonidas1712/aboard/server/internal/rules"

func (s *Service) projectTask(tx ReadTx, b Board, me Member, t *Task) error {
	since := int64(0)
	if t.Stands != nil {
		since = t.Stands.Seq
	}
	messages, threads, after, err := tx.TaskMessageCounts(b.ID, t.ID, me, readsAll(b, me), since)
	if err != nil {
		return err
	}
	t.MessageCount, t.ThreadCount = messages, threads
	if t.Stands != nil {
		t.Stands.MessagesSince = after
	}

	asks, err := tx.Asks(b.ID)
	if err != nil {
		return err
	}
	t.BlockedOn = []BlockedOn{}
	t.BlockedCount = 0
	for _, m := range asks {
		if m.Ask.TaskID == nil || *m.Ask.TaskID != t.ID || !m.Ask.Blocking {
			continue
		}
		if e := projectAsk(tx, me, &m, s.clk.Now()); e != nil {
			return e
		}
		if m.Ask.State != "open" {
			continue
		}
		t.BlockedCount++
		if rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
			t.BlockedOn = append(t.BlockedOn, BlockedOn{m.ID, m.Seq, m.Ask.Target, m.At})
		}
	}
	t.Blocked = t.BlockedCount > 0
	return nil
}
