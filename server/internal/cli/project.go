package cli

import (
	"fmt"
	"path/filepath"
)

// projectFileName is the file in a project directory that names its server and board.
const projectFileName = ".aboard"

// projectFile is the content of .aboard. It never holds a secret or an agent name.
type projectFile struct {
	Server serverRef `json:"server"`
	Board  string    `json:"board"`
}

func (a *app) projectPath() string { return filepath.Join(a.env.Dir, projectFileName) }

// readProject reads .aboard in the working directory. ok is false if there is none.
func (a *app) readProject() (p projectFile, ok bool, err error) {
	ok, err = readJSONFile(a.projectPath(), &p)
	return p, ok, err
}

// writeProject writes .aboard in the working directory.
func (a *app) writeProject(p projectFile) error {
	return writeJSONFile(a.projectPath(), p, 0o644)
}

// linkProject points the working directory at p's board. If it pointed at a different
// board before, it returns that board's name so the command can say so: a directory never
// switches boards without the person seeing it.
func (a *app) linkProject(p projectFile) (previous string, err error) {
	old, found, err := a.readProject()
	if err != nil {
		return "", err
	}
	if err := a.writeProject(p); err != nil {
		return "", err
	}
	if found && (old.Board != p.Board || old.Server.URL != p.Server.URL) {
		return old.Board, nil
	}
	return "", nil
}

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
	source string // where the board came from: boardFromFlag or boardFromProject
}

// Where a command's board came from.
const (
	boardFromFlag    = "flag"
	boardFromProject = "project_file"
)

// sourceText says in words where a board came from.
func sourceText(source string) string {
	switch source {
	case boardFromFlag:
		return "from --board"
	case boardFromAgent:
		return "from the agent"
	}
	return "from ./" + projectFileName
}

// humanBoard picks the board a person's command acts on, as selectBoard does, except
// that in a directory with no .aboard the server is the one resolveServer picks: the
// default server, else the only one this machine knows. With several and no default it
// refuses with server_not_selected rather than guess (D202).
func (a *app) humanBoard(boardFlag string) (target, error) {
	t, err := a.selectBoard(boardFlag)
	if err != nil {
		return target{}, err
	}
	if _, ok, err := a.readProject(); err != nil || ok {
		return t, err
	}
	if t.server, err = a.resolveServer(""); err != nil {
		return target{}, err
	}
	return t, nil
}

// selectBoard picks the board from --board, then .aboard. The server comes from .aboard
// when it names one, otherwise it is the local server.
func (a *app) selectBoard(boardFlag string) (target, error) {
	p, ok, err := a.readProject()
	if err != nil {
		return target{}, err
	}
	t := target{server: a.localServer(), board: boardFlag, source: boardFromFlag}
	if ok && p.Server.URL != "" {
		t.server = p.Server
	}
	if t.board == "" && ok {
		t.board, t.source = p.Board, boardFromProject
	}
	if t.board == "" {
		return target{}, newError("board_not_selected",
			"No board is selected in "+a.env.Dir+".",
			"Run aboard pair to create a board, or aboard join with a join line, in this directory; or pass --board NAME.")
	}
	return t, nil
}
