package board

func projectTask(tx ReadTx, b Board, me Member, t *Task) error {
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
	return nil
}
