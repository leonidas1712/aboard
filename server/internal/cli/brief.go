package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

const briefUsage = "aboard brief [get PATH | put PATH [--base N] [--replace-format]] [--board NAME] [--as AGENT] [--json]"

func runBrief(ctx context.Context, a *app, args []string) error {
	f := a.flags("brief")
	board := f.String("board", "", "the board")
	as := f.String("as", "", "the agent to act as")
	base := f.Int("base", -1, "the fetched version to replace")
	replace := f.Bool("replace-format", false, "replace Markdown with HTML or HTML with Markdown")
	force := f.Bool("force", false, "replace a changed local file")
	pos, err := a.parse(f, args, briefUsage, 0, 2)
	if err != nil {
		return err
	}
	op := ""
	if len(pos) > 0 {
		op = pos[0]
	}
	if (op != "" && op != "get" && op != "put") || (op != "" && len(pos) != 2) || *base < -1 || (op != "put" && (*base != -1 || *replace)) || (op != "get" && *force) {
		return usageError("Read the brief, get it to a local path, or put an edited file back.", briefUsage)
	}
	name := ""
	if op == "put" {
		switch strings.ToLower(filepath.Ext(pos[1])) {
		case ".md", ".markdown":
			name = "brief.md"
		case ".html", ".htm":
			name = "brief.html"
		default:
			return usageError("The brief is Markdown or HTML; pass a .md or .html file.", briefUsage)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	t, c, err := a.taskClient(ctx, *board, *as, "")
	if err != nil {
		return err
	}
	if op == "put" {
		before, err := c.board(ctx, t.board)
		if err != nil {
			return err
		}
		params := api.PutFileParams{Name: name, Brief: ptrTo(true), Maintained: ptrTo(true), ReplaceFormat: replace}
		if *base >= 0 {
			params.Base = base
		}
		item, err := a.uploadLocalFile(ctx, t, c, pos[1], params)
		if err != nil {
			return err
		}
		var replaced *string
		if *replace && before.Brief != nil && before.Brief.Name != nil && string(*before.Brief.Name) != item.Name {
			old := string(*before.Brief.Name)
			replaced = &old
		}
		out := struct {
			Board    string  `json:"board"`
			Name     string  `json:"name"`
			Version  int     `json:"version"`
			Replaced *string `json:"replaced"`
		}{item.Board, item.Name, item.Latest.Version, replaced}
		text := fmt.Sprintf("Put the brief of %s as %s v%d", item.Board, item.Name, item.Latest.Version)
		switch {
		case replaced != nil:
			text += ", replacing " + *replaced + " (its versions stay in the record)."
		case item.Latest.Base > 0:
			text += fmt.Sprintf(" (replaces v%d).", item.Latest.Base)
		default:
			text += "."
		}
		a.emit(out, text+"\n")
		return nil
	}
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	if b.Brief == nil {
		return newError("file_not_found", "There is no brief on "+t.board+".", "Write one with aboard brief put <a .md or .html file> --board "+shellWord(t.board)+".")
	}
	brief := *b.Brief
	detail, err := c.api.GetFileWithResponse(ctx, t.board, brief.FileId)
	if err != nil {
		return c.unreachable(err)
	}
	if detail.JSON200 == nil {
		return apiError(detail.StatusCode(), detail.Body)
	}
	var selected *api.FileVersion
	for i := range detail.JSON200.Versions {
		v := &detail.JSON200.Versions[i]
		if v.Version == brief.Version {
			selected = v
			break
		}
	}
	if selected == nil {
		return newError("file_not_found", "The brief's recorded version is not available.", "Run aboard brief again, or ask the server admin to check storage.")
	}
	r, err := c.api.GetFileVersionWithResponse(ctx, t.board, brief.FileId, strconv.Itoa(brief.Version))
	if err != nil {
		return c.unreachable(err)
	}
	if r.StatusCode() != 200 {
		return apiError(r.StatusCode(), r.Body)
	}
	if bytesDigest(r.Body) != selected.Digest {
		return newError("file_corrupt", "The downloaded bytes do not match the recorded digest.", "Ask the server admin to check storage.")
	}
	briefName := api.BriefSummaryName(detail.JSON200.Name)
	brief.Name = &briefName
	if op == "" {
		a.emit(struct {
			Board string           `json:"board"`
			Brief api.BriefSummary `json:"brief"`
			Text  string           `json:"text"`
		}{t.board, brief, string(r.Body)}, briefLine(t.board, brief)+"\n\n"+string(r.Body))
		return nil
	}
	local := pos[1]
	if local == "-" {
		if a.json {
			a.emit(map[string]any{"board": t.board, "name": detail.JSON200.Name, "version": selected.Version, "digest": selected.Digest, "size": selected.Size, "path": nil}, "")
			return nil
		}
		_, err = a.env.Stdout.Write(r.Body)
		return err
	}
	if !filepath.IsAbs(local) {
		local = filepath.Join(a.env.Dir, local)
	}
	me, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if me.JSON200 == nil {
		return apiError(me.StatusCode(), me.Body)
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	statePath := filepath.Join(p.state, "files.json")
	var entries []fetchedFile
	if err := readJSONFile(statePath, &entries); err != nil {
		return err
	}
	var previous *fetchedFile
	for i := range entries {
		x := &entries[i]
		if x.Server == t.server.URL && x.Actor == me.JSON200.Id && x.Board == b.Id && x.Path == local {
			previous = x
			break
		}
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
	saved := fetchedFile{Server: t.server.URL, Actor: me.JSON200.Id, Board: b.Id, Path: local, ID: brief.FileId, Name: detail.JSON200.Name, Version: selected.Version, Digest: selected.Digest}
	if err := rememberFile(statePath, saved); err != nil {
		return err
	}
	a.emit(map[string]any{"board": t.board, "name": saved.Name, "version": saved.Version, "digest": saved.Digest, "size": selected.Size, "path": local}, fmt.Sprintf("Wrote the brief of %s (%s) to %s.\nPut it back with: aboard brief put %s --board %s\n", t.board, briefDescription(brief), local, shellWord(local), shellWord(t.board)))
	return nil
}

func briefLine(board string, b api.BriefSummary) string {
	return board + " · " + briefDescription(b)
}

func briefDescription(b api.BriefSummary) string {
	name := "brief.md"
	if b.Name != nil {
		name = string(*b.Name)
	}
	return fmt.Sprintf("%s v%d by %s · %s · since then %s, %s done, %s", name, b.Version, b.By.Name, agoText(b.At, time.Now()), counted(b.Freshness.MessagesSince, "message"), counted(b.Freshness.TasksDoneSince, "task"), counted(b.Freshness.AnswersSince, "answer"))
}

func briefJoinHint(board api.Board) string {
	if board.Brief == nil {
		return ""
	}
	return "Read the board's brief with aboard brief --board " + shellWord(board.Name) + ".\n"
}
