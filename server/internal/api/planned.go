package api

import (
	"context"
	"net/http"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// notProvided answers an operation of the API contract that this server doesn't have:
// tasks, asks, agent lines and files. A client reads 501 not_implemented as a server
// without the feature, and says so, rather than as a failure.
func notProvided(what string) error {
	return apierr.New(http.StatusNotImplemented, "not_implemented", "This server doesn't provide "+what+".",
		"Use a server whose GET /v1/info lists the feature.")
}

func (h *handlers) ListTasks(context.Context, ListTasksRequestObject) (ListTasksResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) CreateTask(context.Context, CreateTaskRequestObject) (CreateTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) GetTask(context.Context, GetTaskRequestObject) (GetTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) UpdateTask(context.Context, UpdateTaskRequestObject) (UpdateTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) StartTask(context.Context, StartTaskRequestObject) (StartTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) JoinTask(context.Context, JoinTaskRequestObject) (JoinTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) FinishTask(context.Context, FinishTaskRequestObject) (FinishTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) DropTask(context.Context, DropTaskRequestObject) (DropTaskResponseObject, error) {
	return nil, notProvided("tasks")
}

func (h *handlers) ListAsks(context.Context, ListAsksRequestObject) (ListAsksResponseObject, error) {
	return nil, notProvided("asks")
}

func (h *handlers) SetLine(context.Context, SetLineRequestObject) (SetLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ClearLine(context.Context, ClearLineRequestObject) (ClearLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) SetMemberLine(context.Context, SetMemberLineRequestObject) (SetMemberLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ClearMemberLine(context.Context, ClearMemberLineRequestObject) (ClearMemberLineResponseObject, error) {
	return nil, notProvided("agent lines")
}

func (h *handlers) ListFiles(context.Context, ListFilesRequestObject) (ListFilesResponseObject, error) {
	return nil, notProvided("files")
}

func (h *handlers) PutFile(context.Context, PutFileRequestObject) (PutFileResponseObject, error) {
	return nil, notProvided("files")
}

func (h *handlers) GetFile(context.Context, GetFileRequestObject) (GetFileResponseObject, error) {
	return nil, notProvided("files")
}

func (h *handlers) UpdateFile(context.Context, UpdateFileRequestObject) (UpdateFileResponseObject, error) {
	return nil, notProvided("files")
}

func (h *handlers) GetFileVersion(context.Context, GetFileVersionRequestObject) (GetFileVersionResponseObject, error) {
	return nil, notProvided("files")
}

func (h *handlers) ApproveFile(context.Context, ApproveFileRequestObject) (ApproveFileResponseObject, error) {
	return nil, notProvided("file approvals")
}

func (h *handlers) RemoveFileApproval(context.Context, RemoveFileApprovalRequestObject) (RemoveFileApprovalResponseObject, error) {
	return nil, notProvided("file approvals")
}
