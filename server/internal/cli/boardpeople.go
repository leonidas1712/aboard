package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// handleArg takes a person's handle as typed: with or without its @.
func handleArg(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "@") }

// personClient returns the board a person's command acts on and a client with their
// login, after refusing inside a harness session, where an agent would act as its
// person. what says what the command does; command is the command to hand the person.
func (a *app) personClient(ctx context.Context, boardFlag, what, command string) (target, *client, error) {
	if err := a.refuseInSession(what, command+boardArg(a.namedBoard(boardFlag))); err != nil {
		return target{}, nil, err
	}
	t, err := a.selectBoard(boardFlag)
	if err != nil {
		return target{}, nil, err
	}
	c, err := a.humanClient(ctx, t)
	return t, c, err
}

// runBoardPeople lists the people on a board with their board role. In a harness
// session, or with --as, it reads as the agent, on the agent's board.
func runBoardPeople(ctx context.Context, a *app, boardFlag, asFlag string) error {
	var (
		t   target
		c   *client
		err error
	)
	if _, inSession := a.inSession(); inSession || asFlag != "" {
		var cred agentCredential
		if t, cred, err = a.agentTarget(ctx, boardFlag, asFlag); err != nil {
			return err
		}
		c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
	} else {
		if t, err = a.selectBoard(boardFlag); err != nil {
			return err
		}
		c, err = a.humanClient(ctx, t)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListPeopleWithResponse(ctx, t.board)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	out := r.JSON200
	noun := "people"
	if len(out.People) == 1 {
		noun = "person"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %d %s\n", out.Board, out.Visibility, len(out.People), noun)
	for _, p := range out.People {
		line := "  " + p.Name
		if p.BoardRole == api.BoardRoleOwner {
			line += " (owner)"
		}
		if p.DisplayName != nil {
			line += " · " + *p.DisplayName
		}
		b.WriteString(line + "\n")
	}
	a.emit(out, b.String())
	return nil
}

// boardPersonOutput is the --json output of the commands that change one person on a
// board.
type boardPersonOutput struct {
	Board  string          `json:"board"`
	Person api.BoardPerson `json:"person"`
}

// runBoardAdd adds a person on the server to a board, with the person's own login.
func runBoardAdd(ctx context.Context, a *app, boardFlag, handle string) error {
	t, c, err := a.personClient(ctx, boardFlag, "Adding people to a board", "aboard board add @"+handle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.AddPersonWithResponse(ctx, t.board, nil, api.AddPersonRequest{Handle: handle})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(boardPersonOutput{t.board, *r.JSON201}, fmt.Sprintf("Added %s to %s.\n", handle, t.board))
	return nil
}

// runBoardRemove takes a person off a board; owners only.
func runBoardRemove(ctx context.Context, a *app, boardFlag, handle string) error {
	t, c, err := a.personClient(ctx, boardFlag, "Removing people from a board", "aboard board remove @"+handle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.RemovePersonWithResponse(ctx, t.board, handle, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	text := fmt.Sprintf("Removed %s from %s.\n", handle, t.board)
	if b, err := c.board(ctx, t.board); err == nil && b.Visibility == api.BoardVisibilityOpen {
		text += fmt.Sprintf("%s is open, so %s can join it again; to keep them out, make it private: aboard board visibility private%s\n",
			t.board, handle, boardArg(t.board))
	}
	a.emit(boardPersonOutput{t.board, *r.JSON200}, text)
	return nil
}

// runBoardLeave takes the person off a board.
func runBoardLeave(ctx context.Context, a *app, boardFlag string) error {
	t, c, err := a.personClient(ctx, boardFlag, "Leaving a board for a person", "aboard board leave")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.LeaveBoardWithResponse(ctx, t.board, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(boardPersonOutput{t.board, *r.JSON200}, fmt.Sprintf("You left %s.\n", t.board))
	return nil
}

// runBoardOwner makes a person on a board an owner; owners only.
func runBoardOwner(ctx context.Context, a *app, boardFlag, handle string) error {
	t, c, err := a.personClient(ctx, boardFlag, "Making someone an owner of a board", "aboard board owner @"+handle)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	before, err := c.api.ListPeopleWithResponse(ctx, t.board)
	if err != nil {
		return c.unreachable(err)
	}
	if before.JSON200 == nil {
		return apiError(before.StatusCode(), before.Body)
	}
	was := false
	for _, p := range before.JSON200.People {
		was = was || (p.Handle == handle && p.BoardRole == api.BoardRoleOwner)
	}
	r, err := c.api.AddOwnerWithResponse(ctx, t.board, nil, api.AddPersonRequest{Handle: handle})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	text := fmt.Sprintf("%s is now an owner of %s.\n", handle, t.board)
	if was {
		text = fmt.Sprintf("%s is already an owner of %s.\n", handle, t.board)
	}
	a.emit(struct {
		Board   string          `json:"board"`
		Person  api.BoardPerson `json:"person"`
		Changed bool            `json:"changed"`
	}{t.board, *r.JSON200, !was}, text)
	return nil
}

// runBoardVisibility turns a board open or private; owners only. Turning a private board
// open shows its whole history to every person on the server, so it says how much and
// asks first; without a terminal it needs --yes.
func runBoardVisibility(ctx context.Context, a *app, boardFlag, to string, yes bool) error {
	vis := api.BoardVisibility(to)
	if vis != api.BoardVisibilityOpen && vis != api.BoardVisibilityPrivate {
		return usageError(fmt.Sprintf("%q is not a board visibility; use open or private.", to), boardUsage)
	}
	command := "aboard board visibility " + to
	t, c, err := a.personClient(ctx, boardFlag, "Turning a board open or private", command)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	set := func(dry bool) (*api.VisibilityChange, error) {
		r, err := c.api.SetVisibilityWithResponse(ctx, t.board, nil, api.SetVisibilityRequest{Visibility: vis, DryRun: &dry})
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, apiError(r.StatusCode(), r.Body)
		}
		return r.JSON200, nil
	}
	preview, err := set(true)
	if err != nil {
		return err
	}
	if preview.Changed && preview.Reveals != nil && !yes {
		what := fmt.Sprintf("%s is private. Making it open shows its whole history to every person on %s: %s and %s.",
			t.board, t.server.URL, count(preview.Reveals.Messages, "message"), count(preview.Reveals.Files, "file"))
		if !a.interactive() {
			return newError("confirmation_required", what+" It needs a yes first.",
				"Run "+command+boardArg(t.board)+" --yes to make it open.")
		}
		_, _ = io.WriteString(a.env.Stdout, what+"\n")
		ok, err := a.asker().confirm("Continue?", "", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	out := preview
	if preview.Changed {
		if out, err = set(false); err != nil {
			return err
		}
	}
	var text string
	switch {
	case !out.Changed:
		text = fmt.Sprintf("%s is already %s.\n", out.Board, out.After)
	case out.After == api.BoardVisibilityOpen:
		text = fmt.Sprintf("%s is open now: every person on %s can see it and join it.\n", out.Board, t.server.URL)
	default:
		text = fmt.Sprintf("%s is private now: only the people on it can see it.\n", out.Board)
		if out.JoinCodesCanceled > 0 {
			text += fmt.Sprintf("Canceled %s that still worked.\n", count(out.JoinCodesCanceled, "join code"))
		}
	}
	a.emit(struct {
		Board             string              `json:"board"`
		Before            api.BoardVisibility `json:"before"`
		After             api.BoardVisibility `json:"after"`
		Changed           bool                `json:"changed"`
		Reveals           *api.Reveals        `json:"reveals"`
		JoinCodesCanceled int                 `json:"join_codes_canceled"`
	}{out.Board, out.Before, out.After, out.Changed, out.Reveals, out.JoinCodesCanceled}, text)
	return nil
}

// count says how many of a thing: "1 message", "312 messages".
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
