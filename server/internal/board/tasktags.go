package board

import (
	"errors"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/mention"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func resolveTaskTags(tx ReadTx, b Board, me Member, in NewMessage) ([]TaskTag, error) {
	out := []TaskTag{}
	seen := map[string]bool{}
	add := func(t Task, how string) error {
		if seen[t.ID] {
			return nil
		}
		if len(out) == 8 {
			return apierr.New(http.StatusUnprocessableEntity, "invalid_request", "A message can be about at most eight tasks.", "Use fewer task references in this message.")
		}
		seen[t.ID] = true
		out = append(out, TaskTag{ID: t.ID, Ref: t.Ref, How: how})
		return nil
	}
	switch {
	case in.About != nil:
		for _, selector := range *in.About {
			t, err := findTask(tx, b, selector)
			if err != nil {
				return nil, err
			}
			if err := add(t, "given"); err != nil {
				return nil, err
			}
		}
	case in.ReplyTo != nil:
		orig, err := tx.MessageByID(*in.ReplyTo)
		if err != nil {
			return nil, err
		}
		if !readsAll(b, me) && !rules.CanRead(b.Policy, orig.To, orig.SenderID, me.Rules(), orig.Recipients) {
			break
		}
		for _, tag := range orig.About {
			t, err := findTask(tx, b, tag.ID)
			if err != nil {
				return nil, err
			}
			if err := add(t, "thread"); err != nil {
				return nil, err
			}
		}
	case me.Kind == "agent" && me.CurrentTask != nil:
		t, err := findTask(tx, b, me.CurrentTask.ID)
		if err != nil {
			return nil, err
		}
		if err := add(t, "current"); err != nil {
			return nil, err
		}
	}
	for _, ref := range mention.Tasks(in.Body) {
		t, err := tx.TaskBySelector(b.ID, ref)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := add(t, "named"); err != nil {
			return nil, err
		}
	}
	return out, nil
}
