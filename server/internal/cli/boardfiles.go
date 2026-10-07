package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

const fileUsage = "aboard file list|show NAME|get NAME [PATH]|put PATH|rm NAME|mv NAME NEW [--board NAME] [--as AGENT] [--json]"

type fetchedFile struct {
	Server, Actor, Board, ID, Name, Path, Digest string
	Version                                      int
}

func bytesDigest(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func runFile(ctx context.Context, a *app, args []string) error {
	f := a.flags("file")
	board := f.String("board", "", "the board")
	as := f.String("as", "", "the agent to act as")
	name := f.String("name", "", "the board path to write")
	base := f.Int("base", -1, "the version to replace")
	version := f.Int("version", 0, "the version to download")
	force := f.Bool("force", false, "replace a changed local file")
	task := f.String("task", "", "files linked to this task")
	mine := f.Bool("mine", false, "files you wrote")
	maintained := f.Bool("maintained", false, "keep this file current")
	media := f.String("media-type", "", "the uploaded bytes' media type")
	pos, err := a.parse(f, args, fileUsage, 1, 3)
	if err != nil {
		return err
	}
	op := pos[0]
	maintainedGiven := false
	badFlag := ""
	f.Visit(func(given *flag.Flag) {
		switch given.Name {
		case "json", "as", "board":
		case "name", "base", "maintained", "media-type":
			if op != "put" {
				badFlag = given.Name
			}
			if given.Name == "maintained" {
				maintainedGiven = true
			}
		case "task":
			if op != "put" && op != "list" {
				badFlag = given.Name
			}
		case "mine":
			if op != "list" {
				badFlag = given.Name
			}
		case "version", "force":
			if op != "get" {
				badFlag = given.Name
			}
		}
	})
	if badFlag != "" {
		return usageError("--"+badFlag+" does not apply to file "+op+".", fileUsage)
	}
	if (op == "list" && len(pos) != 1) || ((op == "show" || op == "rm" || op == "put") && len(pos) != 2) || ((op == "mv") && len(pos) != 3) || (op == "get" && len(pos) < 2) || (op != "list" && op != "show" && op != "get" && op != "put" && op != "rm" && op != "mv") || *base < -1 || *version < 0 {
		return usageError("Name a file and an operation.", fileUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	t, c, err := a.taskClient(ctx, *board, *as, "")
	if err != nil {
		return err
	}
	if op == "rm" || op == "mv" {
		var item *api.BoardFile
		if op == "rm" {
			r, e := c.api.RemoveFileWithResponse(ctx, t.board, pos[1], nil)
			if e != nil {
				return c.unreachable(e)
			}
			if r.JSON200 == nil {
				return apiError(r.StatusCode(), r.Body)
			}
			item = r.JSON200
		} else {
			r, e := c.api.UpdateFileWithResponse(ctx, t.board, pos[1], nil, api.UpdateFileRequest{Name: &pos[2]})
			if e != nil {
				return c.unreachable(e)
			}
			if r.JSON200 == nil {
				return apiError(r.StatusCode(), r.Body)
			}
			item = r.JSON200
		}
		text := fmt.Sprintf("Renamed %s to %s on %s.\n", pos[1], item.Name, item.Board)
		if op == "rm" {
			text = fmt.Sprintf("Took %s off %s; its versions stay in the record.\n", item.Name, item.Board)
		}
		a.emit(struct {
			Board string         `json:"board"`
			File  *api.BoardFile `json:"file"`
		}{item.Board, item}, text)
		return nil
	}
	if op == "list" {
		params := api.ListFilesParams{Mine: mine}
		if *task != "" {
			params.Task = task
		}
		r, e := c.api.ListFilesWithResponse(ctx, t.board, &params)
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		text := fmt.Sprintf("%s · %d files\n", r.JSON200.Board, len(r.JSON200.Files))
		for _, item := range r.JSON200.Files {
			text += fmt.Sprintf("  %s · v%d by %s\n", item.Name, item.Latest.Version, item.Latest.By.Name)
		}
		a.emit(r.JSON200, text)
		return nil
	}
	if op == "show" {
		r, e := c.api.GetFileWithResponse(ctx, t.board, pos[1])
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		text := fmt.Sprintf("%s on %s\n", r.JSON200.Name, r.JSON200.Board)
		for _, v := range r.JSON200.Versions {
			text += fmt.Sprintf("  v%d by %s · %s\n", v.Version, v.By.Name, v.At)
		}
		a.emit(struct {
			Board string          `json:"board"`
			File  *api.FileDetail `json:"file"`
		}{r.JSON200.Board, r.JSON200}, text)
		return nil
	}
	me, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if me.JSON200 == nil {
		return apiError(me.StatusCode(), me.Body)
	}
	bmeta, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	statePath := filepath.Join(p.state, "files.json")
	var remembered []fetchedFile
	if _, err := readJSONFile(statePath, &remembered); err != nil {
		return err
	}
	local := pos[1]
	if op == "get" && len(pos) == 3 {
		local = pos[2]
	}
	if !filepath.IsAbs(local) && local != "-" {
		local = filepath.Join(a.env.Dir, local)
	}
	var previous *fetchedFile
	for i := range remembered {
		x := &remembered[i]
		if x.Server == t.server.URL && x.Actor == me.JSON200.Id && x.Board == bmeta.Id && x.Path == local {
			previous = x
			break
		}
	}
	var saved fetchedFile
	saved.Server, saved.Actor, saved.Board, saved.Path = t.server.URL, me.JSON200.Id, bmeta.Id, local
	if op == "put" {
		params := api.PutFileParams{Name: *name}
		if *base >= 0 {
			params.Base = base
		}
		if maintainedGiven {
			params.Maintained = maintained
		}
		if *media != "" {
			params.MediaType = media
		}
		if *task != "" {
			tags := strings.Split(*task, ",")
			params.About = &tags
		}
		item, err := a.uploadLocalFile(ctx, t, c, local, params)
		if err != nil {
			return err
		}
		a.emit(struct {
			Board string         `json:"board"`
			File  *api.BoardFile `json:"file"`
		}{item.Board, item}, fmt.Sprintf("Put %s v%d on %s\n", item.Name, item.Latest.Version, item.Board))
		return nil
	}
	detail, err := c.api.GetFileWithResponse(ctx, t.board, pos[1])
	if err != nil {
		return c.unreachable(err)
	}
	if detail.JSON200 == nil {
		return apiError(detail.StatusCode(), detail.Body)
	}
	selected := detail.JSON200.Latest
	if *version > 0 {
		found := false
		for _, v := range detail.JSON200.Versions {
			if v.Version == *version {
				selected, found = v, true
				break
			}
		}
		if !found {
			return newError("file_not_found", "That version does not exist.", "Run aboard file show to list versions.")
		}
	}
	r, err := c.api.GetFileVersionWithResponse(ctx, t.board, detail.JSON200.Id, strconv.Itoa(selected.Version))
	if err != nil {
		return c.unreachable(err)
	}
	if r.StatusCode() != 200 {
		return apiError(r.StatusCode(), r.Body)
	}
	if bytesDigest(r.Body) != selected.Digest {
		return newError("file_corrupt", "The downloaded bytes do not match the recorded digest.", "Ask the server admin to check storage.")
	}
	if local == "-" {
		if a.json {
			a.emit(map[string]any{"board": detail.JSON200.Board, "name": detail.JSON200.Name, "version": selected.Version, "digest": selected.Digest, "size": selected.Size, "path": nil}, "")
			return nil
		}
		_, err := a.env.Stdout.Write(r.Body)
		return err
	}
	old, err := os.ReadFile(filepath.Clean(local))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil && !*force && (previous == nil || bytesDigest(old) != previous.Digest) {
		return newError("local_file_changed", "The local file has changes aboard did not write.", "Choose another path, or pass --force to replace it.")
	}
	if err := writeFileAtomic(local, r.Body, 0o600); err != nil {
		return err
	}
	saved.ID, saved.Name, saved.Version, saved.Digest = detail.JSON200.Id, detail.JSON200.Name, selected.Version, selected.Digest
	if err := rememberFile(statePath, saved); err != nil {
		return err
	}
	a.emit(map[string]any{"board": detail.JSON200.Board, "name": saved.Name, "version": saved.Version, "digest": saved.Digest, "size": selected.Size, "path": local}, fmt.Sprintf("Wrote %s v%d from %s to %s\n", saved.Name, saved.Version, detail.JSON200.Board, local))
	return nil
}

func rememberFile(path string, saved fetchedFile) error {
	var entries []fetchedFile
	return updateJSONFile(path, &entries, func() error {
		for i, entry := range entries {
			if entry.Server == saved.Server && entry.Actor == saved.Actor && entry.Board == saved.Board && entry.Path == saved.Path {
				entries[i] = saved
				return nil
			}
		}
		entries = append(entries, saved)
		return nil
	})
}

// uploadLocalFile shares version and identity fencing with say --attach.
func (a *app) uploadLocalFile(ctx context.Context, t target, c *client, local string, params api.PutFileParams) (*api.BoardFile, error) {
	if !filepath.IsAbs(local) {
		local = filepath.Join(a.env.Dir, local)
	}
	me, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if me.JSON200 == nil {
		return nil, apiError(me.StatusCode(), me.Body)
	}
	bmeta, err := c.board(ctx, t.board)
	if err != nil {
		return nil, err
	}
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	statePath := filepath.Join(p.state, "files.json")
	var entries []fetchedFile
	if _, err := readJSONFile(statePath, &entries); err != nil {
		return nil, err
	}
	var previous *fetchedFile
	for i := range entries {
		x := &entries[i]
		if x.Server == t.server.URL && x.Actor == me.JSON200.Id && x.Board == bmeta.Id && x.Path == local {
			previous = x
			break
		}
	}
	if params.Name == "" && previous != nil {
		params.Name = previous.Name
	}
	if params.Name == "" {
		params.Name = filepath.Base(local)
	}
	if params.Base == nil {
		base := 0
		if previous != nil && previous.Name == params.Name {
			base = previous.Version
		}
		params.Base = &base
	}
	if previous != nil && previous.Name == params.Name {
		params.FileId = &previous.ID
	}
	file, err := os.Open(filepath.Clean(local))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 50*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 50*1024*1024 {
		return nil, newError("file_too_large", "Files can be at most 50 MB.", "Upload a smaller file.")
	}
	r, err := c.api.PutFileWithBodyWithResponse(ctx, t.board, &params, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON201 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	item := r.JSON201
	saved := fetchedFile{Server: t.server.URL, Actor: me.JSON200.Id, Board: bmeta.Id, Path: local, ID: item.Id, Name: item.Name, Version: item.Latest.Version, Digest: item.Latest.Digest}
	if err := rememberFile(statePath, saved); err != nil {
		return nil, err
	}
	return item, nil
}
