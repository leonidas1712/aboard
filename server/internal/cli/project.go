package cli

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/api"
	"path/filepath"
)

// projectFileName is the deprecated folder-link filename.
const projectFileName = ".aboard"

// projectFile is the content of .aboard. It never holds a secret or an agent name.
type projectFile struct {
	Server serverRef `json:"server"`
	Board  string    `json:"board"`
}

func (a *app) projectPath() string { return filepath.Join(a.env.Dir, projectFileName) }

// Legacy folder links are ignored. The machine default and explicit flags select servers.
func (a *app) readProject() (projectFile, bool, error) { return projectFile{}, false, nil }

// writeProject remains for legacy test fixtures; commands no longer call it.
func (a *app) writeProject(p projectFile) error {
	return writeJSONFile(a.projectPath(), p, 0o644)
}

func (a *app) linkProject(_ projectFile) (string, error) { return "", nil }

// relinkedText is the line a command prints after moving the directory to another board.
func relinkedText(board, previous string) string {
	if previous == "" {
		return ""
	}
	return fmt.Sprintf("Linked this directory to board %s (it was linked to %s).\n", board, previous)
}

// target is the server and board a command acts on.
type target struct {
	server serverRef
	board  string
	source string
}

// Where a command's board came from.
const (
	boardFromOnly    = "only"
	boardFromFlag    = "flag"
	boardFromProject = "project_file"
)

// sourceText says in words where a board came from.
func sourceText(source string) string {
	switch source {
	case boardFromOnly:
		return "the only readable active board"
	case boardFromFlag:
		return "from --board"
	case boardFromAgent:
		return "from the agent"
	}
	return "from ./" + projectFileName
}

// humanBoard uses an explicit board or the sole readable active board on the selected issuer.
func (a *app) humanBoard(ctx context.Context, boardFlag string) (target, error) {
	srv, err := a.resolveServer(a.selectedServerFlag())
	if err != nil {
		return target{}, err
	}
	t := target{server: srv, board: boardFlag, source: boardFromFlag}
	if boardFlag != "" {
		return t, nil
	}
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return target{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	all := true
	r, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: &all})
	if err != nil {
		return target{}, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return target{}, apiError(r.StatusCode(), r.Body)
	}
	choices := []string{}
	for _, b := range r.JSON200.Boards {
		if b.Lifecycle != nil && *b.Lifecycle != api.BoardLifecycleActive {
			continue
		}
		choices = append(choices, b.Name)
	}
	if len(choices) == 1 {
		t.board, t.source = choices[0], boardFromOnly
		return t, nil
	}
	code, message := "board_not_selected", "No readable active board is available on this server."
	if len(choices) > 1 {
		code, message = "board_ambiguous", "Several readable boards are available; name one with --board."
	}
	e := newError(code, message, "Run aboard boards --server "+commandWord(srv.URL)+", then repeat with --board NAME.")
	e.Details = map[string]any{"boards": choices, "server": srv.URL}
	return target{}, e
}

// selectBoard resolves an explicit board without reading folder state.
func (a *app) selectBoard(boardFlag string) (target, error) {
	srv, err := a.resolveServer(a.selectedServerFlag())
	if err != nil {
		return target{}, err
	}
	if boardFlag == "" {
		return target{}, newError("board_not_selected", "No board was named.", "Run aboard boards, then repeat with --board NAME.")
	}
	return target{server: srv, board: boardFlag, source: boardFromFlag}, nil
}
