package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// humanClient returns a client for the board's server with the local owner's login.
func (a *app) humanClient(ctx context.Context, t target) (*client, error) {
	token, err := a.readOwnerToken(t.server)
	if err != nil {
		return nil, err
	}
	return a.client(ctx, t.server, token, requestTimeout)
}

// boardArg returns the --board flag to repeat in a suggested command, if one was given.
func boardArg(board string) string {
	if board == "" {
		return ""
	}
	return " --board " + shellWord(board)
}

// namedBoard returns the board a human command would use, for naming it in a command
// to hand to a person: boardFlag, else the board selected the usual way. It returns
// boardFlag unchanged when no board can be selected.
func (a *app) namedBoard(boardFlag string) string {
	if t, err := a.selectBoard(boardFlag); err == nil {
		return t.board
	}
	return boardFlag
}

// boardUsage is the usage of "aboard board", which acts on a board's settings.
var boardUsage = usageOf("board")

// runBoard runs "aboard board policy <preset>", which switches a board's policy preset,
// "aboard board title <text>", which changes its title, and the commands for a board's
// people, visibility and lifecycle. Those that change policy, people or visibility, and
// delete, use the human login, so they refuse inside a harness session; an agent may set
// the title for its owner, list a board's people, and archive or restore its own board.
func runBoard(ctx context.Context, a *app, args []string) error {
	fs := a.flags("board")
	boardFlag := fs.String("board", "", "the board to change")
	as := fs.String("as", "", "the agent that sets the title, for its owner")
	yes := fs.Bool("yes", false, "visibility and delete only: go ahead without asking")
	pos, err := a.parse(fs, args, boardUsage, 1, -1)
	if err != nil {
		return err
	}
	onePerson := func() (string, error) {
		if len(pos) != 2 || handleArg(pos[1]) == "" {
			return "", usageError("Name one person, by handle: aboard board "+pos[0]+" @maya.", boardUsage)
		}
		if *as != "" {
			return "", usageError("Only a person changes who is on a board, so --as works only with title, people, archive and restore.", boardUsage)
		}
		return handleArg(pos[1]), nil
	}
	switch pos[0] {
	case "people":
		if len(pos) != 1 {
			return usageError("aboard board people takes no arguments.", boardUsage)
		}
		return runBoardPeople(ctx, a, *boardFlag, *as)
	case "add", "remove", "owner":
		handle, err := onePerson()
		if err != nil {
			return err
		}
		switch pos[0] {
		case "add":
			return runBoardAdd(ctx, a, *boardFlag, handle)
		case "remove":
			return runBoardRemove(ctx, a, *boardFlag, handle)
		}
		return runBoardOwner(ctx, a, *boardFlag, handle)
	case "leave":
		if len(pos) != 1 || *as != "" {
			return usageError("aboard board leave takes no arguments and no --as: it is for a person.", boardUsage)
		}
		return runBoardLeave(ctx, a, *boardFlag)
	case "visibility":
		if len(pos) != 2 {
			return usageError("Say open or private: aboard board visibility private.", boardUsage)
		}
		if *as != "" {
			return usageError("Only a person turns a board open or private, so --as works only with title, people, archive and restore.", boardUsage)
		}
		return runBoardVisibility(ctx, a, *boardFlag, pos[1], *yes)
	case "archive", "restore", "delete":
		if len(pos) > 2 {
			return usageError("Name one board: aboard board "+pos[0]+" payments-design.", boardUsage)
		}
		sel := *boardFlag
		if len(pos) == 2 {
			if sel != "" && sel != pos[1] {
				return usageError("Name the board once: as the argument or with --board, not both.", boardUsage)
			}
			sel = pos[1]
		}
		if pos[0] == "delete" {
			return runBoardDelete(ctx, a, sel, *as, *yes)
		}
		if *yes {
			return usageError("--yes works only with visibility and delete.", boardUsage)
		}
		return runBoardArchive(ctx, a, sel, *as, pos[0] == "restore")
	}
	if *yes {
		return usageError("--yes works only with visibility and delete.", boardUsage)
	}
	switch pos[0] {
	case "policy":
		if len(pos) != 2 {
			return usageError("Name one policy preset: starter or recommended.", boardUsage)
		}
		if *as != "" {
			return usageError("Only a person changes a board's policy, so --as works only with title.", boardUsage)
		}
		return runBoardPolicy(ctx, a, *boardFlag, pos[1])
	case "title":
		if len(pos) < 2 {
			return usageError("Give the new title, or \"\" to remove it.", boardUsage)
		}
		return runBoardTitle(ctx, a, *boardFlag, *as, strings.Join(pos[1:], " "))
	}
	return usageError(fmt.Sprintf("%q is not a board command; use people, add, remove, leave, owner, visibility, policy, title, archive, restore or delete.", pos[0]), boardUsage)
}

// runBoardTitle changes a board's title; an empty title removes it. When an agent is
// selected (--as, ABOARD_AGENT or a harness session), the agent sets it for its owner, on the agent's board, and the
// record names the agent; elsewhere the person sets it with their own login.
func runBoardTitle(ctx context.Context, a *app, boardFlag, asFlag, title string) error {
	title = strings.TrimSpace(title)
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
	before, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	r, err := c.api.UpdateBoardWithResponse(ctx, t.board, &api.UpdateBoardParams{}, api.UpdateBoardRequest{Title: &title})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	after := r.JSON200
	text := fmt.Sprintf("Board %s has no title now.\n", after.Name)
	if after.Title != nil {
		text = fmt.Sprintf("Board %s is now titled %q.\n", after.Name, *after.Title)
	}
	a.emit(struct {
		Board  string  `json:"board"`
		Before *string `json:"before"`
		After  *string `json:"after"`
	}{after.Name, before.Title, after.Title}, text)
	return nil
}

// runBoardPolicy switches a board's policy preset.
func runBoardPolicy(ctx context.Context, a *app, boardFlag, presetArg string) error {
	preset := api.PolicyPreset(presetArg)
	if preset != "starter" && preset != "recommended" {
		return usageError(fmt.Sprintf("%q is not a policy preset; use starter or recommended.", presetArg), boardUsage)
	}
	if err := a.refuseInSession("Changing a board's policy", "aboard board policy "+string(preset)+boardArg(a.namedBoard(boardFlag))); err != nil {
		return err
	}
	t, err := a.selectBoard(boardFlag)
	if err != nil {
		return err
	}
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	before, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	r, err := c.api.UpdateBoardWithResponse(ctx, t.board, &api.UpdateBoardParams{},
		api.UpdateBoardRequest{Policy: &api.PolicyChange{Preset: &preset}})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	after := r.JSON200
	notice := noticeFor(after.Policy)
	text := fmt.Sprintf("Board %s is now on the %s policy.\n", after.Name, after.Policy.Preset)
	if notice != nil {
		text += notice.Message + "\n"
	}
	a.emit(struct {
		Board        string        `json:"board"`
		Before       api.Policy    `json:"before"`
		After        api.Policy    `json:"after"`
		PolicyNotice *policyNotice `json:"policy_notice"`
	}{after.Name, before.Policy, after.Policy, notice}, text)
	return nil
}
