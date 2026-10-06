package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// boardIDPattern is a board's permanent id, which a server admin sees in hidden_boards.
var boardIDPattern = regexp.MustCompile(`^brd_[0-9A-HJKMNP-TV-Z]{26}$`)

// boardLifecycleOutput is aboard board archive, restore and delete's --json output
// (BoardLifecycleOutput in spec/cli.yaml): a receipt of what the server committed.
type boardLifecycleOutput struct {
	Server    serverRef          `json:"server"`
	Board     string             `json:"board"`
	ID        string             `json:"id"`
	Lifecycle api.LifecycleState `json:"lifecycle"`
	Changed   bool               `json:"changed"`
}

// runBoardArchive archives (restore false) or restores a board. A person uses their own
// login; an agent selected by --as, ABOARD_AGENT or its session uses its own seat's
// token, on its own board, and never the person's login.
func runBoardArchive(ctx context.Context, a *app, sel, asFlag string, restore bool) error {
	var (
		t   target
		c   *client
		err error
	)
	if a.agentSelected(asFlag) {
		var cred agentCredential
		if t, cred, err = a.agentTarget(ctx, sel, asFlag); err != nil {
			return err
		}
		c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
	} else {
		if t, err = a.selectBoard(sel); err != nil {
			return err
		}
		c, err = a.humanClient(ctx, t)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var (
		body   []byte
		status int
		out    *api.BoardLifecycleResult
	)
	if restore {
		r, err := c.api.RestoreBoardWithResponse(ctx, t.board, &api.RestoreBoardParams{})
		if err != nil {
			return c.unreachable(err)
		}
		body, status, out = r.Body, r.StatusCode(), r.JSON200
	} else {
		r, err := c.api.ArchiveBoardWithResponse(ctx, t.board, &api.ArchiveBoardParams{})
		if err != nil {
			return c.unreachable(err)
		}
		body, status, out = r.Body, r.StatusCode(), r.JSON200
	}
	if out == nil {
		return apiError(status, body)
	}
	name, restoreCmd := t.board, "aboard board restore "+shellWord(t.board)
	var text string
	switch {
	case !restore && out.Changed:
		text = fmt.Sprintf("Archived %s. It's read-only now; restore with: %s.\n", name, restoreCmd)
	case !restore:
		text = fmt.Sprintf("%s is already archived; restore with: %s.\n", name, restoreCmd)
	case out.Changed:
		text = fmt.Sprintf("Restored %s. New messages and joins work again.\n", name)
	default:
		text = fmt.Sprintf("%s isn't archived; nothing changed.\n", name)
	}
	a.emit(boardLifecycleOutput{t.server, t.board, out.Id, out.Lifecycle, out.Changed}, text)
	return nil
}

// runBoardDelete deletes an archived board for good: its record stays, but nobody can
// open it again. It is a person's, with their own login. In a terminal it asks for the
// board's name typed exactly, or its id when the board was named by id, so it never
// reads a private board an admin isn't on; elsewhere it needs --yes.
func runBoardDelete(ctx context.Context, a *app, sel, asFlag string, yes bool) error {
	named := sel
	if named == "" {
		named = a.namedBoard("")
	}
	command := "aboard board delete"
	if named != "" {
		command += " " + shellWord(named)
	}
	if asFlag != "" {
		return newError("human_command_in_session",
			"Deleting a board is up to a person, and --as says this command runs as the agent "+asFlag+".",
			"Give your human this command to run in their own terminal: "+command)
	}
	if err := a.refuseInSession("Deleting a board", command); err != nil {
		return err
	}
	t, err := a.selectBoard(sel)
	if err != nil {
		return err
	}
	command = "aboard board delete " + shellWord(t.board)
	if !yes && !a.interactive() {
		return newError("confirmation_required",
			"Deleting "+t.board+" ends every way into it for good. It needs a yes first.",
			"Run "+command+" --yes to delete it.")
	}
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	byID := boardIDPattern.MatchString(t.board)
	if !byID {
		// Say an active board needs archiving before asking for its name. A board this
		// person can't read is left to the server, which answers as for any other.
		if b, err := c.board(ctx, t.board); err == nil && (b.Lifecycle == nil || *b.Lifecycle != api.BoardLifecycleArchived) {
			return newError("board_not_archived",
				t.board+" isn't archived, and only an archived board can be deleted.",
				"Archive it first with aboard board archive "+shellWord(t.board)+", then delete it.")
		}
	}
	if !yes {
		what := "name"
		if byID {
			what = "id"
		}
		_, _ = io.WriteString(a.env.Stdout, "Deleting "+t.board+" ends every way into it for good: "+
			"its people and agents lose it, its join codes stop, and nobody can open or restore it. Its record is kept.\n")
		typed, err := a.asker().text("Type the board's "+what+" to delete it", "", "")
		if errors.Is(err, errAborted) || (err == nil && strings.TrimSpace(typed) != t.board) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed: that isn't the board's "+what+".\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	r, err := c.api.DeleteBoardWithResponse(ctx, t.board, &api.DeleteBoardParams{})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	out := r.JSON200
	a.emit(boardLifecycleOutput{t.server, t.board, out.Id, out.Lifecycle, out.Changed},
		fmt.Sprintf("Deleted %s. Its record is kept; nobody can open it again.\n", t.board))
	return nil
}
