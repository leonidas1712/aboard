package cli

import (
	"context"
	"fmt"

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

// runBoard runs "aboard board policy <preset>", which switches a board's policy preset.
// It uses the human login, so it refuses inside a harness session.
func runBoard(ctx context.Context, a *app, args []string) error {
	const use = "aboard board policy <starter|recommended> [--board NAME] [--json]"
	fs := a.flags("board")
	boardFlag := fs.String("board", "", "the board to change")
	pos, err := a.parse(fs, args, use, 2, 2)
	if err != nil {
		return err
	}
	if pos[0] != "policy" {
		return usageError(fmt.Sprintf("%q is not a board command.", pos[0]), use)
	}
	preset := api.PolicyPreset(pos[1])
	if preset != "starter" && preset != "recommended" {
		return usageError(fmt.Sprintf("%q is not a policy preset; use starter or recommended.", pos[1]), use)
	}
	if err := a.refuseInSession("Changing a board's policy", "aboard board policy "+string(preset)+boardArg(a.namedBoard(*boardFlag))); err != nil {
		return err
	}
	t, err := a.selectBoard(*boardFlag)
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
		api.UpdateBoardRequest{Policy: api.PolicyChange{Preset: &preset}})
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
