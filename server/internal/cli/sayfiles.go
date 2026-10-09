package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func resolveSayFiles(ctx context.Context, c *client, board string, selectors []string) ([]api.FileVersionSelector, error) {
	files := make([]api.FileVersionSelector, 0, len(selectors))
	hint := "Run aboard file list --board " + shellWord(board) + ", or upload a local file with --attach."
	for _, selector := range selectors {
		name, suffix, explicit := strings.Cut(selector, "@v")
		version := 0
		if explicit {
			var err error
			version, err = strconv.Atoi(suffix)
			if err != nil || version < 1 || name == "" {
				return nil, newError("invalid_request", "Invalid file version in "+selector+".", "Use --file NAME or --file NAME@vN, with a positive version number.")
			}
		}
		r, err := c.api.GetFileWithResponse(ctx, board, name)
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			err := apiError(r.StatusCode(), r.Body)
			var e *Error
			if errors.As(err, &e) && e.Code == "file_not_found" {
				e.Hint = hint
			}
			return nil, err
		}
		if !explicit {
			version = r.JSON200.Latest.Version
		} else {
			found := false
			for _, v := range r.JSON200.Versions {
				if v.Version == version {
					found = true
					break
				}
			}
			if !found {
				return nil, newError("file_not_found", fmt.Sprintf("File %q has no version %d on %s.", name, version, board), hint)
			}
		}
		files = append(files, api.FileVersionSelector{File: r.JSON200.Id, Version: &version})
	}
	return files, nil
}

func (a *app) sayFiles(ctx context.Context, c *client, t target, selectors, localPaths []string) ([]api.FileVersionSelector, error) {
	files, err := resolveSayFiles(ctx, c, t.board, selectors)
	if err != nil {
		return nil, err
	}
	for _, local := range localPaths {
		item, err := a.uploadLocalFile(ctx, t, c, local, api.PutFileParams{})
		if err != nil {
			return nil, err
		}
		files = append(files, api.FileVersionSelector{File: item.Id, Version: &item.Latest.Version})
	}
	seen := make(map[struct {
		id      string
		version int
	}]bool)
	unique := files[:0]
	for _, file := range files {
		key := struct {
			id      string
			version int
		}{file.File, *file.Version}
		if !seen[key] {
			unique = append(unique, file)
			seen[key] = true
		}
	}
	return unique, nil
}
