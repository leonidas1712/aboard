package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// TaskRef identifies a task independently of its current title.
type TaskRef struct{ ID, Ref, Title string }

// TaskText is one recorded version of About or Where it stands.
type TaskText struct {
	Seq           int64
	MessagesSince int
	Text          string
	By            Member
	At            string
	Version       int64
}

// Task is the read model of the task's recorded changes.
type Task struct {
	Blocked                               bool
	BlockedCount                          int
	BlockedOn                             []BlockedOn
	MessageCount, ThreadCount             int
	ID, Ref, BoardID, Board, Title, State string
	Number                                int64
	About, Stands                         *TaskText
	Owner                                 *Member
	Helpers                               []Member
	OpenedBy                              Member
	OpenedAt, UpdatedAt                   string
	ClosedAt, ClosedNote                  *string
}

// NewTask opens a task, optionally taking it at once.
type NewTask struct {
	Title, About string
	Start        bool
}

// TaskUpdate replaces only the fields supplied.
type TaskUpdate struct {
	Title, About, Stands *string
	StandsBase           *int64
}

// TaskFinish records why a task closed.
type TaskFinish struct {
	Note      string
	Cancelled bool
}

// TaskDrop releases a member's part in a task.
type TaskDrop struct{ Member, Reason string }

// TaskFilter selects tasks without changing their order.
type TaskFilter struct {
	Done, All, Mine bool
	State, Owner    string
	Limit           int
}

// TaskListing contains the selected tasks and counts before pagination.
type TaskListing struct {
	Board  Board
	Tasks  []Task
	Counts map[string]int
	More   bool
}

func taskError(status int, code, message string) error {
	return apierr.New(status, code, message, "Read the task with aboard task show, or list tasks with aboard task list.")
}

func taskPermission(b Board, me Member, permission string) error {
	if me.Kind == "human" {
		return nil
	}
	if me.Role != nil && b.Roles[*me.Role].Has(permission) {
		return nil
	}
	return apierr.New(http.StatusForbidden, "forbidden", "Your role needs "+permission+" to do this.", "Ask a person on the board to grant your role "+permission+".")
}

func taskTextValid(s string, limit int, required bool) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit || (required && strings.TrimSpace(s) == "") {
		return invalid(fmt.Sprintf("Use text with %d characters or fewer.", limit), "Supply nonempty text within the limit.")
	}
	return nil
}

func taskTitleValid(title string) error {
	if e := taskTextValid(title, 120, true); e != nil {
		return e
	}
	if strings.ContainsAny(title, "\r\n") {
		return invalid("A task title must be one line.", "Remove the line breaks from the title.")
	}
	return nil
}
func taskRef(t Task) TaskRef { return TaskRef{ID: t.ID, Ref: t.Ref, Title: t.Title} }
func taskPart(t Task, id string) string {
	if t.Owner != nil && t.Owner.ID == id {
		return "owner"
	}
	for _, m := range t.Helpers {
		if m.ID == id {
			return "helper"
		}
	}
	return ""
}

func taskClosed(t Task) error {
	if t.State == "done" || t.State == "cancelled" {
		return taskError(http.StatusConflict, "task_closed", "This task is already closed.")
	}
	return nil
}

func findTask(tx ReadTx, b Board, sel string) (Task, error) {
	t, e := tx.TaskBySelector(b.ID, sel)
	if errors.Is(e, ErrNotFound) {
		return Task{}, taskError(http.StatusNotFound, "task_not_found", "This task isn't on this board.")
	}
	return t, e
}

// CreateTask appends creation and optional ownership in one write.
func (s *Service) CreateTask(ctx context.Context, p Principal, name string, in NewTask) (Task, error) {
	if e := taskTitleValid(in.Title); e != nil {
		return Task{}, e
	}
	if e := taskTextValid(in.About, 4000, false); e != nil {
		return Task{}, e
	}
	var out Task
	e := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		if e := requireActive(b); e != nil {
			return e
		}
		if e := taskPermission(b, me, rules.CreateTasks); e != nil {
			return e
		}
		if in.Start {
			if e := taskPermission(b, me, rules.ClaimTasks); e != nil {
				return e
			}
		}
		now := s.clk.Now()
		if b.TaskPrefix == nil {
			prefix := autoTaskPrefix(b.Name)
			for i := 1; ; i++ {
				candidate := prefix
				if i > 1 {
					candidate = prefix + fmt.Sprint(i)
				}
				if len(candidate) > 6 {
					return taskError(http.StatusConflict, "task_prefix_taken", "No automatic task prefix is available. Set a prefix for this board.")
				}
				owner, e := tx.TaskPrefixOwner(candidate)
				if errors.Is(e, ErrNotFound) || e == nil && owner == b.ID {
					if e := s.setTaskPrefix(tx, &b, me, candidate, now); e != nil {
						return e
					}
					break
				}
				if e != nil {
					return e
				}
			}
		}
		number, e := tx.NextTaskNumber(b.ID)
		if e != nil {
			return e
		}
		id, e := s.gen.ID("tsk", now)
		if e != nil {
			return e
		}
		out = Task{ID: id, Ref: fmt.Sprintf("%s-%d", *b.TaskPrefix, number), BoardID: b.ID, Board: b.Name, Number: number, Title: in.Title, State: "open", Helpers: []Member{}, OpenedBy: me, OpenedAt: stamp(now), UpdatedAt: stamp(now)}
		var about any
		if in.About != "" {
			out.About = &TaskText{Text: in.About, By: me, At: stamp(now), Version: 1}
			about = in.About
		}
		if _, e = s.append(tx, &b, events.TaskCreated, actorOf(me), now, map[string]any{"task_id": id, "ref": out.Ref, "number": number, "title": in.Title, "about": about}); e != nil {
			return e
		}
		if e := tx.SaveTask(out); e != nil {
			return e
		}
		if in.Start {
			if e := s.takeTask(tx, &b, me, &out, false, now); e != nil {
				return e
			}
		}
		return s.projectTask(tx, b, me, &out)
	})
	if e != nil {
		return Task{}, e
	}
	s.notify.Changed(out.BoardID)
	return out, nil
}

func autoTaskPrefix(name string) string {
	var s strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' {
			s.WriteRune(r)
			if s.Len() == 3 {
				break
			}
		}
	}
	for s.Len() < 2 {
		s.WriteByte('X')
	}
	return s.String()
}

var taskPrefixPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,5}$`)

// SetTaskPrefix reserves a prefix forever; old task references remain unchanged.
func (s *Service) SetTaskPrefix(ctx context.Context, p Principal, name, prefix string) (Board, error) {
	view, err := s.UpdateBoard(ctx, p, name, Change{TaskPrefix: &prefix})
	return view.Board, err
}

func (s *Service) setTaskPrefix(tx Tx, b *Board, me Member, prefix string, now time.Time) error {
	if deref(b.TaskPrefix) == prefix {
		return nil
	}
	owner, e := tx.TaskPrefixOwner(prefix)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return e
	}
	if e == nil && owner != b.ID {
		return taskError(http.StatusConflict, "task_prefix_taken", "This task prefix is already in use.")
	}
	if _, e = s.append(tx, b, events.BoardTaskPrefixSet, actorOf(me), now, map[string]any{"before": b.TaskPrefix, "after": prefix}); e != nil {
		return e
	}
	if e := tx.ReserveTaskPrefix(b.ID, prefix); e != nil {
		return e
	}
	b.TaskPrefix = ptr(prefix)
	return tx.SetTaskPrefix(b.ID, prefix)
}

func (s *Service) takeTask(tx Tx, b *Board, me Member, t *Task, join bool, now time.Time) error {
	if e := taskClosed(*t); e != nil {
		return e
	}
	if e := taskPermission(*b, me, rules.ClaimTasks); e != nil {
		return e
	}
	part := taskPart(*t, me.ID)
	reselected := part != ""
	if !join {
		reselected = part == "owner"
	}
	if !join && t.Owner != nil && t.Owner.ID != me.ID {
		return taskError(http.StatusConflict, "task_taken", "This task is owned by "+t.Owner.Name+".")
	}
	if reselected && (me.Kind == "human" || me.CurrentTask != nil && me.CurrentTask.ID == t.ID) {
		return nil
	}
	typ := events.TaskStarted
	data := map[string]any{"task_id": t.ID, "ref": t.Ref, "member_id": me.ID}
	if join {
		typ = events.TaskJoined
		if part == "" {
			t.Helpers = append(t.Helpers, me)
		}
	} else {
		data["previous_owner"] = nil
		t.Owner = &me
		t.State = "in_progress"
		t.Helpers = slices.DeleteFunc(t.Helpers, func(m Member) bool { return m.ID == me.ID })
	}
	if reselected {
		data["reselected"] = true
	}
	if _, e := s.append(tx, b, typ, actorOf(me), now, data); e != nil {
		return e
	}
	t.UpdatedAt = stamp(now)
	if e := tx.SaveTask(*t); e != nil {
		return e
	}
	if me.Kind == "agent" {
		ref := taskRef(*t)
		return tx.SetCurrentTask(me.ID, &ref)
	}
	return nil
}

// StartTask atomically takes ownership and selects the current task.
func (s *Service) StartTask(ctx context.Context, p Principal, name, sel string) (Task, error) {
	return s.selectTask(ctx, p, name, sel, false)
}

// JoinTask adds the caller as helper and selects the current task.
func (s *Service) JoinTask(ctx context.Context, p Principal, name, sel string) (Task, error) {
	return s.selectTask(ctx, p, name, sel, true)
}

func (s *Service) selectTask(ctx context.Context, p Principal, name, sel string, join bool) (Task, error) {
	var out Task
	e := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		if e := requireActive(b); e != nil {
			return e
		}
		out, e = findTask(tx, b, sel)
		if e != nil {
			return e
		}
		if e := s.takeTask(tx, &b, me, &out, join, s.clk.Now()); e != nil {
			return e
		}
		return s.projectTask(tx, b, me, &out)
	})
	if e == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, e
}

// UpdateTask records changed text with authorship and version checks.
func (s *Service) UpdateTask(ctx context.Context, p Principal, name, sel string, in TaskUpdate) (Task, error) {
	if in.Title != nil {
		if e := taskTitleValid(*in.Title); e != nil {
			return Task{}, e
		}
	}
	if in.Title == nil && in.About == nil && in.Stands == nil {
		return Task{}, invalid("Supply a task field to change.", "Set title, about or stands.")
	}
	for _, v := range []struct {
		s        *string
		max      int
		required bool
	}{{in.Title, 120, true}, {in.About, 4000, false}, {in.Stands, 2000, true}} {
		if v.s != nil {
			if e := taskTextValid(*v.s, v.max, v.required); e != nil {
				return Task{}, e
			}
		}
	}
	var out Task
	e := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		if e := requireActive(b); e != nil {
			return e
		}
		out, e = findTask(tx, b, sel)
		if e != nil {
			return e
		}
		if e := taskClosed(out); e != nil {
			return e
		}
		part := taskPart(out, me.ID)
		if me.Kind != "human" && ((in.Title != nil || in.About != nil) && out.OpenedBy.ID != me.ID && part != "owner" || in.Stands != nil && part == "") {
			return taskError(http.StatusForbidden, "not_on_task", "You may not change these fields on this task.")
		}
		version := int64(0)
		if out.Stands != nil {
			version = out.Stands.Version
		}
		if in.StandsBase != nil && *in.StandsBase != version {
			return taskError(http.StatusConflict, "stands_changed", "Where it stands has changed since that version.")
		}
		now := s.clk.Now()
		data := map[string]any{"task_id": out.ID, "ref": out.Ref}
		if in.Title != nil && *in.Title != out.Title {
			out.Title = *in.Title
			data["title"] = *in.Title
		}
		if in.About != nil && (out.About == nil || out.About.Text != *in.About) {
			v := int64(1)
			if out.About != nil {
				v = out.About.Version + 1
			}
			out.About = &TaskText{Text: *in.About, By: me, At: stamp(now), Version: v}
			data["about"] = *in.About
		}
		if in.Stands != nil && (out.Stands == nil || out.Stands.Text != *in.Stands) {
			out.Stands = &TaskText{Text: *in.Stands, By: me, At: stamp(now), Version: version + 1}
			data["stands"] = *in.Stands
			data["stands_version"] = version + 1
		}
		if len(data) == 2 {
			return s.projectTask(tx, b, me, &out)
		}
		event, e := s.append(tx, &b, events.TaskUpdated, actorOf(me), now, data)
		if e != nil {
			return e
		}
		if _, changed := data["stands"]; changed {
			out.Stands.Seq = event.Seq
		}
		out.UpdatedAt = stamp(now)
		if e := tx.SaveTask(out); e != nil {
			return e
		}
		return s.projectTask(tx, b, me, &out)
	})
	if e == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, e
}

// FinishTask closes the task and clears only members whose current task is this one.
func (s *Service) FinishTask(ctx context.Context, p Principal, name, sel string, in TaskFinish) (Task, error) {
	if e := taskTextValid(in.Note, 2000, true); e != nil {
		return Task{}, e
	}
	var out Task
	e := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		if e := requireActive(b); e != nil {
			return e
		}
		out, e = findTask(tx, b, sel)
		if e != nil {
			return e
		}
		if e := taskClosed(out); e != nil {
			return e
		}
		if me.Kind != "human" && taskPart(out, me.ID) != "owner" {
			return taskError(http.StatusForbidden, "not_on_task", "Only the owner or a person may close this task.")
		}
		now := s.clk.Now()
		if _, e = s.append(tx, &b, events.TaskDone, actorOf(me), now, map[string]any{"task_id": out.ID, "ref": out.Ref, "note": in.Note, "cancelled": in.Cancelled}); e != nil {
			return e
		}
		out.State = "done"
		if in.Cancelled {
			out.State = "cancelled"
		}
		out.ClosedAt = ptr(stamp(now))
		out.ClosedNote = ptr(in.Note)
		out.UpdatedAt = stamp(now)
		if e := tx.ClearTaskCurrent(out.ID); e != nil {
			return e
		}
		if e := tx.SaveTask(out); e != nil {
			return e
		}
		return s.projectTask(tx, b, me, &out)
	})
	if e == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, e
}

// DropTask releases the caller, or a member named by a person on the board.
func (s *Service) DropTask(ctx context.Context, p Principal, name, sel string, in TaskDrop) (Task, error) {
	if e := taskTextValid(in.Reason, 500, false); e != nil {
		return Task{}, e
	}
	var out Task
	e := s.writeAs(ctx, p, func(tx Tx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		if e := requireActive(b); e != nil {
			return e
		}
		out, e = findTask(tx, b, sel)
		if e != nil {
			return e
		}
		if e := taskClosed(out); e != nil {
			return e
		}
		target := me
		if in.Member != "" {
			target, e = tx.MemberByID(in.Member)
			if errors.Is(e, ErrNotFound) {
				target, e = tx.MemberByName(b.ID, in.Member)
			}
			if e != nil || target.BoardID != b.ID {
				return taskError(http.StatusNotFound, "member_not_found", "This member isn't on this board.")
			}
		}
		if me.Kind != "human" && target.ID != me.ID {
			return taskError(http.StatusForbidden, "not_on_task", "An agent can drop only its own part.")
		}
		by := "self"
		if target.ID != me.ID {
			by = "person"
		}
		if e := s.dropTask(tx, &b, &out, target.ID, in.Reason, by, actorOf(me), s.clk.Now()); e != nil {
			return e
		}
		return s.projectTask(tx, b, me, &out)
	})
	if e == nil {
		s.notify.Changed(out.BoardID)
	}
	return out, e
}

func (s *Service) dropTask(tx Tx, b *Board, t *Task, member, reason, by string, actor events.Actor, now time.Time) error {
	part := taskPart(*t, member)
	if part == "" {
		return taskError(http.StatusForbidden, "not_on_task", "This member isn't working on this task.")
	}
	if _, e := s.append(tx, b, events.TaskDropped, actor, now, map[string]any{"task_id": t.ID, "ref": t.Ref, "member_id": member, "as": part, "reason": reason, "by": by}); e != nil {
		return e
	}
	if part == "owner" {
		t.Owner = nil
		t.State = "open"
	} else {
		t.Helpers = slices.DeleteFunc(t.Helpers, func(m Member) bool { return m.ID == member })
	}
	t.UpdatedAt = stamp(now)
	if e := tx.ClearMemberTask(member, t.ID); e != nil {
		return e
	}
	return tx.SaveTask(*t)
}

func (s *Service) dropSeatTasks(tx Tx, b *Board, member string, actor events.Actor, now time.Time) error {
	tasks, e := tx.Tasks(b.ID)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if taskPart(t, member) != "" && taskClosed(t) == nil {
			if e := s.dropTask(tx, b, &t, member, "Seat ended.", "seat_ended", actor, now); e != nil {
				return e
			}
		}
	}
	return nil
}

// GetTask reads a task within the caller's current board access.
func (s *Service) GetTask(ctx context.Context, p Principal, name, sel string) (Task, error) {
	var out Task
	e := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		out, e = findTask(tx, b, sel)
		if e != nil {
			return e
		}
		return s.projectTask(tx, b, me, &out)
	})
	return out, e
}

// ListTasks orders open work before owned work, then closed tasks.
func (s *Service) ListTasks(ctx context.Context, p Principal, name string, f TaskFilter) (TaskListing, error) {
	if f.State != "" && !slices.Contains([]string{"all", "open", "in_progress", "done", "cancelled"}, f.State) {
		return TaskListing{}, invalid("This task state isn't supported.", "Use open, in_progress, done, cancelled or all.")
	}
	var out TaskListing
	e := s.st.Read(ctx, func(tx ReadTx) error {
		b, me, e := s.access(tx, p, name)
		if e != nil {
			return e
		}
		out.Board = b
		all, e := tx.Tasks(b.ID)
		if e != nil {
			return e
		}
		out.Counts = map[string]int{"open": 0, "in_progress": 0, "done": 0, "cancelled": 0, "blocked": 0}
		out.Tasks = []Task{}
		for _, t := range all {
			out.Counts[t.State]++
			if !f.All && !f.Done && f.State == "" && (t.State == "done" || t.State == "cancelled") {
				continue
			}
			if f.State != "" && f.State != "all" && t.State != f.State {
				continue
			}
			if f.Owner != "" && (t.Owner == nil || t.Owner.ID != f.Owner && t.Owner.Name != f.Owner) {
				continue
			}
			if f.Mine && taskPart(t, me.ID) == "" {
				continue
			}
			if e := s.projectTask(tx, b, me, &t); e != nil {
				return e
			}
			out.Tasks = append(out.Tasks, t)
		}
		slices.SortStableFunc(out.Tasks, func(a, b Task) int {
			rank := func(t Task) int {
				switch t.State {
				case "open":
					return 0
				case "in_progress":
					return 1
				default:
					return 2
				}
			}
			if d := rank(a) - rank(b); d != 0 {
				return d
			}
			if a.Number < b.Number {
				return -1
			}
			if a.Number > b.Number {
				return 1
			}
			return 0
		})
		if f.Limit > 0 && len(out.Tasks) > f.Limit {
			out.More = true
			out.Tasks = out.Tasks[:f.Limit]
		}
		return nil
	})
	return out, e
}
