package board

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// WorkingTask is the current task as its agent sees it with the inbox.
type WorkingTask struct {
	TaskRef
	Owner  bool
	Stands *TaskText
}

// AgentWork contains recorded task facts; reading it never changes work or cursors.
type AgentWork struct {
	AsksWaiting, AsksToIt int
	CurrentTask           *WorkingTask
	OpenTasks             int64
	OldestOpen            *TaskRef
	PostsWithoutTask      int
	Nudges                bool
}

// TaskPostReader counts the member's latest consecutive posts without a task.
type TaskPostReader interface {
	PostsWithoutTask(memberID string) (int, error)
}

// TaskWork reads the agent's task context under its current credential and membership.
func (s *Service) TaskWork(ctx context.Context, p Principal) (AgentWork, error) {
	var out AgentWork
	e := s.st.Read(ctx, func(tx ReadTx) error {
		if p.Agent == nil {
			return apierr.AgentRequired()
		}
		if e := stillValid(tx, p, stamp(s.clk.Now())); e != nil {
			return e
		}
		b, me, e := seatOf(tx, *p.Agent)
		if e != nil {
			return e
		}
		out, e = s.taskWork(tx, b, me)
		return e
	})
	return out, e
}

func (s *Service) taskWork(tx ReadTx, b Board, me Member) (AgentWork, error) {
	out := AgentWork{Nudges: b.Policy.Nudges != "off"}
	tasks, e := tx.Tasks(b.ID)
	if e != nil {
		return out, e
	}
	for _, t := range tasks {
		if t.State == "open" {
			out.OpenTasks++
			if out.OldestOpen == nil {
				ref := taskRef(t)
				out.OldestOpen = &ref
			}
		}
		if me.CurrentTask != nil && me.CurrentTask.ID == t.ID {
			if e := s.projectTask(tx, b, me, &t); e != nil {
				return out, e
			}
			out.CurrentTask = &WorkingTask{TaskRef: taskRef(t), Owner: t.Owner != nil && t.Owner.ID == me.ID, Stands: t.Stands}
		}
	}
	asks, e := tx.Asks(b.ID)
	if e != nil {
		return out, e
	}
	for _, m := range asks {
		if e := projectAsk(tx, me, &m, s.clk.Now()); e != nil {
			return out, e
		}
		if m.Ask.State == "open" {
			if m.SenderID == me.ID {
				out.AsksWaiting++
			}
			if m.Ask.To == me.ID {
				out.AsksToIt++
			}
		}
	}
	if out.CurrentTask == nil {
		if reader, ok := tx.(TaskPostReader); ok {
			out.PostsWithoutTask, e = reader.PostsWithoutTask(me.ID)
		}
	}
	return out, e
}
