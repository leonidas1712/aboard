package api

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
)

func fileVersionOf(v board.FileVersion) any {
	return map[string]any{"version": v.Version, "base": v.Base, "digest": v.Digest, "size": v.Size, "media_type": v.MediaType, "by": refOf(v.By), "at": v.At, "seq": v.Seq}
}

func fileOf(f board.File) map[string]any {
	about := []any{}
	for _, t := range f.About {
		v := t
		about = append(about, taskRefOf(&v))
	}
	return map[string]any{"id": f.ID, "name": f.Name, "board": f.Board, "maintained": f.Maintained, "about": about, "latest": fileVersionOf(f.Versions[len(f.Versions)-1]), "approvals": []any{}, "mine": nil, "freshness": map[string]any{"messages_since": f.MessagesSince, "tasks_done_since": f.TasksDoneSince, "answers_since": f.AnswersSince}}
}

func (h *handlers) PutFile(ctx context.Context, req PutFileRequestObject) (PutFileResponseObject, error) {
	in := board.NewFile{Brief: req.Params.Brief != nil && *req.Params.Brief, ReplaceFormat: req.Params.ReplaceFormat != nil && *req.Params.ReplaceFormat, FileID: req.Params.FileId, Name: req.Params.Name, Maintained: req.Params.Maintained, About: req.Params.About, Body: req.Body}
	if req.Params.Base != nil {
		in.Base = *req.Params.Base
	}
	if req.Params.MediaType != nil {
		in.MediaType = *req.Params.MediaType
	}
	f, err := h.svc.PutFile(ctx, principal(ctx), req.Board, in)
	if err != nil {
		return nil, err
	}
	return convert[PutFile201JSONResponse](fileOf(f))
}

func (h *handlers) ListFiles(ctx context.Context, req ListFilesRequestObject) (ListFilesResponseObject, error) {
	filter := board.FileFilter{Limit: limitOr(req.Params.Limit)}
	if req.Params.Task != nil {
		filter.Task = *req.Params.Task
	}
	if req.Params.Mine != nil {
		filter.Mine = *req.Params.Mine
	}
	fs, more, err := h.svc.ListFiles(ctx, principal(ctx), req.Board, filter)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, f := range fs {
		out = append(out, fileOf(f))
	}
	return convert[ListFiles200JSONResponse](map[string]any{"board": req.Board, "files": out, "more": more})
}

func (h *handlers) GetFile(ctx context.Context, req GetFileRequestObject) (GetFileResponseObject, error) {
	f, err := h.svc.GetFile(ctx, principal(ctx), req.Board, req.File)
	if err != nil {
		return nil, err
	}
	out := fileOf(f)
	vs := []any{}
	for i := len(f.Versions) - 1; i >= 0; i-- {
		vs = append(vs, fileVersionOf(f.Versions[i]))
	}
	out["versions"] = vs
	out["posted_in"] = f.PostedIn
	return convert[GetFile200JSONResponse](out)
}

func (h *handlers) GetFileVersion(ctx context.Context, req GetFileVersionRequestObject) (GetFileVersionResponseObject, error) {
	v, name, r, err := h.svc.FileBytes(ctx, principal(ctx), req.Board, req.File, req.Version)
	if err != nil {
		return nil, err
	}
	etag := `"` + v.Digest + `"`
	return fileDownload{body: r, size: v.Size, mediaType: v.MediaType, etag: etag, name: name}, nil
}

type fileDownload struct {
	body                  io.ReadCloser
	size                  int64
	mediaType, etag, name string
}

func (h *handlers) RemoveFile(ctx context.Context, req RemoveFileRequestObject) (RemoveFileResponseObject, error) {
	f, err := h.svc.ChangeFile(ctx, principal(ctx), req.Board, req.File, board.FileChange{}, true)
	if err != nil {
		return nil, err
	}
	return convert[RemoveFile200JSONResponse](fileOf(f))
}

func (h *handlers) UpdateFile(ctx context.Context, req UpdateFileRequestObject) (UpdateFileResponseObject, error) {
	if req.Body == nil {
		return nil, apierr.New(400, "invalid_request", "Name a file change.", "Send name, maintained or about.")
	}
	f, err := h.svc.ChangeFile(ctx, principal(ctx), req.Board, req.File, board.FileChange{Name: req.Body.Name, Maintained: req.Body.Maintained, About: req.Body.About}, false)
	if err != nil {
		return nil, err
	}
	return convert[UpdateFile200JSONResponse](fileOf(f))
}

func (d fileDownload) VisitGetFileVersionResponse(w http.ResponseWriter) error {
	defer func() { _ = d.body.Close() }()
	w.Header().Set("Content-Type", d.mediaType)
	w.Header().Set("Content-Length", fmt.Sprint(d.size))
	w.Header().Set("ETag", d.etag)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(d.name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, d.body)
	return err
}
