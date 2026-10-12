package api

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"

	"github.com/leonidas1712/aboard/server/internal/board"
	boardexport "github.com/leonidas1712/aboard/server/internal/export"
)

func (h *handlers) ExportBoard(ctx context.Context, req ExportBoardRequestObject) (ExportBoardResponseObject, error) {
	p := principal(ctx)
	view, err := h.svc.GetBoard(ctx, p, req.Board)
	if err != nil {
		return nil, err
	}
	d := boardexport.Document{Board: view.Board, Server: h.svc.Config().IssuerURL, At: h.clk.Now()}
	var after int64
	for {
		page, err := h.svc.Timeline(ctx, p, req.Board, board.TimelineFilter{After: after, Before: view.Board.HeadSeq + 1, Limit: 500})
		if err != nil {
			return nil, err
		}
		if d.By == "" {
			d.By = "@" + page.Reader.Name
		}
		d.Messages = append(d.Messages, page.Messages...)
		if page.NextAfter == nil || *page.NextAfter >= view.Board.HeadSeq {
			break
		}
		after = *page.NextAfter
	}
	tasks, err := h.svc.ListTasks(ctx, p, req.Board, board.TaskFilter{All: true})
	if err != nil {
		return nil, err
	}
	d.Tasks = tasks.Tasks
	d.Files, _, err = h.svc.ListFiles(ctx, p, req.Board, board.FileFilter{})
	if err != nil {
		return nil, err
	}
	open := func(f board.File) (io.ReadCloser, error) {
		v := f.Versions[len(f.Versions)-1]
		_, _, r, err := h.svc.FileBytes(ctx, p, req.Board, f.ID, strconv.Itoa(v.Version))
		return r, err
	}
	if view.Brief != nil {
		d.BriefName = view.Brief.Name
		r, err := open(*view.Brief)
		if err != nil {
			return nil, err
		}
		d.Brief, err = io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return nil, err
		}
	}
	format := "md"
	if req.Params.Format != nil {
		format = string(*req.Params.Format)
	}
	// Build before sending headers so an unreadable blob returns an ordinary API error.
	f, err := os.CreateTemp("", "aboard-export-*")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	mediaType := "text/markdown; charset=utf-8"
	if format == "zip" {
		mediaType = "application/zip"
		err = d.Zip(f, open)
	} else {
		_, err = f.WriteString(d.Markdown())
	}
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	keep = true
	return exportDownload{f, stat.Size(), mediaType, view.Board.Name + "-" + d.At.UTC().Format("2006-01-02") + "." + format}, nil
}

type exportDownload struct {
	body            *os.File
	size            int64
	mediaType, name string
}

func (d exportDownload) VisitExportBoardResponse(w http.ResponseWriter) error {
	defer func() {
		_ = d.body.Close()
		// The path comes from CreateTemp, never from the requested board or file name.
		_ = os.Remove(d.body.Name()) // #nosec G703
	}()
	w.Header().Set("Content-Type", d.mediaType)
	w.Header().Set("Content-Length", fmt.Sprint(d.size))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, d.body)
	return err
}
