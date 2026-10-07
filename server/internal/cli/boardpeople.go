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

// commandWord is s as one word of a command for a person to paste into a shell: as it
// is when it holds only characters no shell treats specially, such as a handle or a
// server URL, and otherwise quoted.
func commandWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+-./:_,=") == "" {
		return s
	}
	return shellWord(s)
}

// personClient returns the board a person's command acts on and a client with their
// login, after refusing inside a harness session, where an agent would act as its
// person. what says what the command does; command is the command to hand the person.
func (a *app) personClient(ctx context.Context, boardFlag, what, command string) (target, *client, error) {
	if err := a.refuseInSession(what, command+a.boardFlags(boardFlag)); err != nil {
		return target{}, nil, err
	}
	t, err := a.personBoard(boardFlag)
	if err != nil {
		return target{}, nil, err
	}
	c, err := a.humanClient(ctx, t)
	return t, c, err
}

// boardFlags are the --board and --server flags to repeat in a command handed to the
// person, quoted for a shell, so it acts on the same board on the same server: with
// --server, the board given; otherwise the one this directory names.
func (a *app) boardFlags(boardFlag string) string {
	if a.boardServerFlag == "" {
		return boardArg(a.namedBoard(boardFlag))
	}
	return boardArg(boardFlag) + a.serverArg()
}

// serverArg is the --server flag to repeat in a suggested command, if one was given.
func (a *app) serverArg() string {
	if a.boardServerFlag == "" {
		return ""
	}
	return " --server " + commandWord(a.boardServerFlag)
}

// personBoard is the board a person's board command acts on: with --server, the board
// --board names on that server; otherwise as humanBoard picks it.
func (a *app) personBoard(boardFlag string) (target, error) {
	if a.boardServerFlag == "" {
		return a.humanBoard(boardFlag)
	}
	if boardFlag == "" {
		return target{}, usageError("--server needs --board, naming a board on that server.", boardUsage)
	}
	srv, err := parseServerURL(a.boardServerFlag)
	if err != nil {
		return target{}, err
	}
	return target{server: a.serverRefFor(srv.URL), board: boardFlag, source: boardFromFlag}, nil
}

// agentSelected reports whether a command that can act as a person or as an agent acts
// as an agent: when one is named by --as or ABOARD_AGENT, or the command runs inside a
// harness session. Then it must use the agent's token, never the person's login.
func (a *app) agentSelected(asFlag string) bool {
	_, inSession := a.inSession()
	return asFlag != "" || strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")) != "" || inSession
}

// runBoardPeople lists the people on a board with their board role. When an agent is
// selected (--as, ABOARD_AGENT or a harness session) it reads as the agent, on the
// agent's board.
func runBoardPeople(ctx context.Context, a *app, boardFlag, asFlag string) error {
	var (
		t   target
		c   *client
		err error
	)
	if a.agentSelected(asFlag) {
		var cred agentCredential
		if t, cred, err = a.agentTarget(ctx, boardFlag, asFlag); err != nil {
			return err
		}
		c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
	} else {
		if t, err = a.humanBoard(boardFlag); err != nil {
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
	out := boardPeopleOutput{Board: r.JSON200.Board, Visibility: r.JSON200.Visibility, People: []personWithAgents{}}
	// The agents come from the member list, read with the same credential: a caller who
	// may list a board's people but not its members (a person outside an open board)
	// sees no agents.
	var members []api.Member
	if m, err := c.api.ListMembersWithResponse(ctx, t.board, nil); err != nil {
		return c.unreachable(err)
	} else if m.JSON200 != nil {
		members = m.JSON200.Members
	}
	noun := "people"
	if len(r.JSON200.People) == 1 {
		noun = "person"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %d %s\n", out.Board, out.Visibility, len(r.JSON200.People), noun)
	for _, p := range r.JSON200.People {
		line := "  " + p.Name
		if p.BoardRole == api.BoardRoleOwner {
			line += " (owner)"
		}
		if p.ServerRole == api.ServerRoleGuest {
			line += " (guest)"
		}
		if p.DisplayName != nil {
			line += " · " + *p.DisplayName
		}
		b.WriteString(line + "\n")
		row := personWithAgents{BoardPerson: p}
		if members != nil {
			row.Agents = []personAgent{}
			for _, m := range members {
				if m.Kind != api.MemberKindAgent || m.OwnerId == nil || *m.OwnerId != p.Id {
					continue
				}
				ag := personAgent{Name: m.Name, Harness: m.Harness}
				parts := []string{"@" + m.Name}
				if m.Harness != nil {
					parts = append(parts, *m.Harness)
				}
				if m.Presence != nil {
					s := string(*m.Presence)
					ag.Presence = &s
					parts = append(parts, presenceText(s))
				}
				row.Agents = append(row.Agents, ag)
				b.WriteString("    " + strings.Join(parts, " · ") + "\n")
			}
		}
		out.People = append(out.People, row)
	}
	a.emit(out, b.String())
	return nil
}

// boardPeopleOutput is aboard board people's --json output (cli.yaml, BoardPeopleOutput).
type boardPeopleOutput struct {
	Board      api.BoardName       `json:"board"`
	Visibility api.BoardVisibility `json:"visibility"`
	People     []personWithAgents  `json:"people"`
}

// personWithAgents is one person on the board with their agents on it; Agents is nil
// when the caller can't read the board's members.
type personWithAgents struct {
	api.BoardPerson
	Agents []personAgent `json:"agents"`
}

type personAgent struct {
	Name     string  `json:"name"`
	Harness  *string `json:"harness"`
	Presence *string `json:"presence"`
}

// boardPersonOutput is the --json output of the commands that change one person on a
// board.
type boardPersonOutput struct {
	Board  string          `json:"board"`
	Person api.BoardPerson `json:"person"`
}

func runBoardAddAs(ctx context.Context, a *app, boardFlag, handle, as string) error {
	var t target
	var c *client
	var err error
	byAgent, byOwner := "", ""
	if a.agentSelected(as) {
		var cred agentCredential
		t, cred, err = a.agentTarget(ctx, boardFlag, as)
		if err == nil && a.boardServerFlag != "" {
			selected, parseErr := parseServerURL(a.boardServerFlag)
			if parseErr != nil {
				return parseErr
			}
			if selected.URL != t.server.URL {
				return newError("agent_not_selected", "The selected agent's board is on "+t.server.URL+", not "+selected.URL+".", "Use --server "+commandWord(t.server.URL)+" for this seat, or select a seat on the other server.")
			}
		}
		if err == nil {
			c, err = a.client(ctx, t.server, cred.Token, requestTimeout)
		}
		if err == nil {
			rctx, cancel := a.requestContext(ctx)
			me, e := c.api.GetMeWithResponse(rctx)
			cancel()
			if e != nil {
				return c.unreachable(e)
			}
			if me.JSON200 == nil {
				return apiError(me.StatusCode(), me.Body)
			}
			byAgent, byOwner = cred.Name, deref(me.JSON200.Owner)
			if handle == "me" {
				handle = byOwner
			}
		}
	} else {
		t, c, err = a.personClient(ctx, boardFlag, "Adding people to a board", "aboard board add "+commandWord("@"+handle))
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	// @me is the person running the command: joining an open board.
	if handle == "me" {
		m, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if m.JSON200 == nil {
			return apiError(m.StatusCode(), m.Body)
		}
		handle = m.JSON200.Name
	}
	r, err := c.api.AddPersonWithResponse(ctx, t.board, nil, api.AddPersonRequest{Handle: handle})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		refusal := apiError(r.StatusCode(), r.Body)
		if byAgent != "" {
			var e *Error
			if errors.As(refusal, &e) {
				switch e.Code {
				case "add_people_not_allowed", "agent_session_required", "guest_not_allowed", "person_is_guest":
					e.Hint = "Your person runs aboard board add " + commandWord("@"+handle) + " --board " + commandWord(t.board) + " --server " + commandWord(t.server.URL) + " in a terminal."
				}
			}
		}
		return refusal
	}
	text := fmt.Sprintf("Added %s to %s.\n", handle, t.board)
	if byAgent != "" {
		text = fmt.Sprintf("Added %s to %s (by %s, for %s).\n", handle, t.board, byAgent, byOwner)
	}
	a.emit(struct {
		Board   string          `json:"board"`
		Person  api.BoardPerson `json:"person"`
		ByAgent *string         `json:"by_agent,omitempty"`
		ByOwner *string         `json:"by_owner,omitempty"`
	}{t.board, *r.JSON201, optional(byAgent), optional(byOwner)}, text)
	return nil
}

// runBoardRemove takes a person off a board; owners only.
func runBoardRemove(ctx context.Context, a *app, boardFlag, handle string) error {
	t, c, err := a.personClient(ctx, boardFlag, "Removing people from a board", "aboard board remove "+commandWord("@"+handle))
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
	t, c, err := a.personClient(ctx, boardFlag, "Making someone an owner of a board", "aboard board owner "+commandWord("@"+handle))
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
	command := "aboard board visibility " + commandWord(to)
	// The command handed to the person never carries --yes: they decide, and see what
	// opening the board reveals before they confirm.
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
			t.board, t.server.URL, counted(preview.Reveals.Messages, "message"), counted(preview.Reveals.Files, "file"))
		if !a.interactive() {
			return newError("confirmation_required", what+" It needs a yes first.",
				"Run "+command+boardArg(t.board)+a.serverArg()+" --yes to make it open.")
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
			text += fmt.Sprintf("Canceled %s that still worked.\n", counted(out.JoinCodesCanceled, "join code"))
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
