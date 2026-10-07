package api

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func taskWorkOf(w *board.AgentWork) any {
	if w == nil {
		return nil
	}
	var current any
	if w.CurrentTask != nil {
		t := w.CurrentTask
		current = map[string]any{"id": t.ID, "ref": t.Ref, "title": t.Title, "owner": t.Owner, "stands": taskTextOf(t.Stands)}
	}
	return map[string]any{
		"current_task": current, "open_tasks": w.OpenTasks, "oldest_open": taskRefOf(w.OldestOpen), "posts_without_task": w.PostsWithoutTask,
		"asks_waiting": w.AsksWaiting, "asks_to_it": w.AsksToIt, "line": nil, "brief": nil, "nudges": w.Nudges,
	}
}

func taskRefOf(t *board.TaskRef) any {
	if t == nil {
		return nil
	}
	return map[string]any{"id": t.ID, "ref": t.Ref, "title": t.Title}
}

func taskTextOf(t *board.TaskText) any {
	if t == nil {
		return nil
	}
	return map[string]any{"text": t.Text, "by": refOf(t.By), "at": t.At, "version": t.Version, "messages_since": t.MessagesSince}
}

func taskOf(t board.Task) map[string]any {
	var owner any
	if t.Owner != nil {
		owner = refOf(*t.Owner)
	}
	helpers := make([]wireMemberRef, 0, len(t.Helpers))
	for _, m := range t.Helpers {
		helpers = append(helpers, refOf(m))
	}
	return map[string]any{
		"id": t.ID, "ref": t.Ref, "number": t.Number, "board": t.Board,
		"title": t.Title, "about": taskTextOf(t.About), "stands": taskTextOf(t.Stands),
		"state": t.State, "owner": owner, "with": helpers,
		"blocked": t.Blocked, "blocked_count": t.BlockedCount, "blocked_on": blockedOf(t.BlockedOn),
		"opened_by": refOf(t.OpenedBy), "opened_at": t.OpenedAt, "updated_at": t.UpdatedAt,
		"closed_at": t.ClosedAt, "closed_note": t.ClosedNote,
		"message_count": t.MessageCount, "thread_count": t.ThreadCount,
	}
}

func (h *handlers) ListTasks(ctx context.Context, req ListTasksRequestObject) (ListTasksResponseObject, error) {
	f := board.TaskFilter{Limit: limitOr(req.Params.Limit)}
	if req.Params.State != nil {
		f.State = string(*req.Params.State)
	}
	if req.Params.Owner != nil {
		f.Owner = *req.Params.Owner
	}
	if req.Params.Mine != nil {
		f.Mine = *req.Params.Mine
	}
	v, err := h.svc.ListTasks(ctx, principal(ctx), req.Board, f)
	if err != nil {
		return nil, err
	}
	tasks := make([]map[string]any, 0, len(v.Tasks))
	for _, t := range v.Tasks {
		tasks = append(tasks, taskOf(t))
	}
	return convert[ListTasks200JSONResponse](map[string]any{"board": v.Board.Name, "tasks": tasks, "counts": v.Counts, "more": v.More})
}

func (h *handlers) CreateTask(ctx context.Context, req CreateTaskRequestObject) (CreateTaskResponseObject, error) {
	in := board.NewTask{Title: req.Body.Title}
	if req.Body.About != nil {
		in.About = *req.Body.About
	}
	if req.Body.Start != nil {
		in.Start = *req.Body.Start
	}
	t, err := h.svc.CreateTask(ctx, principal(ctx), req.Board, in)
	if err != nil {
		return nil, err
	}
	return convert[CreateTask201JSONResponse](taskOf(t))
}

func (h *handlers) GetTask(ctx context.Context, req GetTaskRequestObject) (GetTaskResponseObject, error) {
	t, err := h.svc.GetTask(ctx, principal(ctx), req.Board, req.Task)
	if err != nil {
		return nil, err
	}
	return convert[GetTask200JSONResponse](taskOf(t))
}

func (h *handlers) UpdateTask(ctx context.Context, req UpdateTaskRequestObject) (UpdateTaskResponseObject, error) {
	in := board.TaskUpdate{Title: req.Body.Title, About: req.Body.About, Stands: req.Body.Stands}
	if req.Body.StandsBase != nil {
		n := int64(*req.Body.StandsBase)
		in.StandsBase = &n
	}
	t, err := h.svc.UpdateTask(ctx, principal(ctx), req.Board, req.Task, in)
	if err != nil {
		return nil, err
	}
	return convert[UpdateTask200JSONResponse](taskOf(t))
}

func (h *handlers) StartTask(ctx context.Context, req StartTaskRequestObject) (StartTaskResponseObject, error) {
	t, err := h.svc.StartTask(ctx, principal(ctx), req.Board, req.Task)
	if err != nil {
		return nil, err
	}
	return convert[StartTask200JSONResponse](taskOf(t))
}

func (h *handlers) JoinTask(ctx context.Context, req JoinTaskRequestObject) (JoinTaskResponseObject, error) {
	t, err := h.svc.JoinTask(ctx, principal(ctx), req.Board, req.Task)
	if err != nil {
		return nil, err
	}
	return convert[JoinTask200JSONResponse](taskOf(t))
}

func (h *handlers) FinishTask(ctx context.Context, req FinishTaskRequestObject) (FinishTaskResponseObject, error) {
	in := board.TaskFinish{Note: req.Body.Note}
	if req.Body.Cancelled != nil {
		in.Cancelled = *req.Body.Cancelled
	}
	t, err := h.svc.FinishTask(ctx, principal(ctx), req.Board, req.Task, in)
	if err != nil {
		return nil, err
	}
	return convert[FinishTask200JSONResponse](taskOf(t))
}

func (h *handlers) DropTask(ctx context.Context, req DropTaskRequestObject) (DropTaskResponseObject, error) {
	in := board.TaskDrop{}
	if req.Body != nil {
		if req.Body.Member != nil {
			in.Member = *req.Body.Member
		}
		if req.Body.Reason != nil {
			in.Reason = *req.Body.Reason
		}
	}
	t, err := h.svc.DropTask(ctx, principal(ctx), req.Board, req.Task, in)
	if err != nil {
		return nil, err
	}
	return convert[DropTask200JSONResponse](taskOf(t))
}
