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
// and "aboard board title <text>", which changes its title. Both use the human login, so
// they refuse inside a harness session.
func runBoard(ctx context.Context, a *app, args []string) error {
	fs := a.flags("board")
	boardFlag := fs.String("board", "", "the board to change")
	pos, err := a.parse(fs, args, boardUsage, 1, -1)
	if err != nil {
		return err
	}
	switch pos[0] {
	case "policy":
		if len(pos) != 2 {
			return usageError("Name one policy preset: starter or recommended.", boardUsage)
		}
		return runBoardPolicy(ctx, a, *boardFlag, pos[1])
	case "title":
		if len(pos) < 2 {
			return usageError("Give the new title, or \"\" to remove it.", boardUsage)
		}
		return runBoardTitle(ctx, a, *boardFlag, strings.Join(pos[1:], " "))
	}
	return usageError(fmt.Sprintf("%q is not a board command; use policy or title.", pos[0]), boardUsage)
}

// runBoardTitle changes a board's title; an empty title removes it.
func runBoardTitle(ctx context.Context, a *app, boardFlag, title string) error {
	title = strings.TrimSpace(title)
	if err := a.refuseInSession("Changing a board's title", "aboard board title "+shellWord(title)+boardArg(a.namedBoard(boardFlag))); err != nil {
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
