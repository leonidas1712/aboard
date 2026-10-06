package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func runBoardAgentsAddPeople(ctx context.Context, a *app, boardFlag, value string, yes bool) error {
	if value != "on" && value != "off" {
		return usageError("Say on or off: aboard board agents-add-people on.", boardUsage)
	}
	enabled := value == "on"
	command := "aboard board agents-add-people " + value
	t, c, err := a.personClient(ctx, boardFlag, "Allowing agents to add people", command)
	if err != nil {
		return err
	}
	rctx, cancel := a.requestContext(ctx)
	b, err := c.api.GetBoardWithResponse(rctx, t.board)
	cancel()
	if err != nil {
		return c.unreachable(err)
	}
	if b.JSON200 == nil {
		return apiError(b.StatusCode(), b.Body)
	}
	if enabled && b.JSON200.Visibility == api.BoardVisibilityPrivate && !yes {
		warning := fmt.Sprintf("Allowing agents to add people to %s gives anyone they add its whole history and files.", t.board)
		if !a.interactive() {
			return newError("confirmation_required", warning, "Run "+command+a.boardFlags(boardFlag)+" --yes to continue.")
		}
		_, _ = io.WriteString(a.env.Stdout, warning+"\n")
		ok, err := a.asker().confirm("Continue?", "", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	rctx, cancel = a.requestContext(ctx)
	defer cancel()
	r, err := c.api.UpdateBoardWithResponse(rctx, t.board, nil, api.UpdateBoardRequest{AgentsAddPeople: &enabled})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	effective := enabled
	if r.JSON200.AgentsAddPeople != nil {
		effective = *r.JSON200.AgentsAddPeople
	}
	text := fmt.Sprintf("Agents may not add people to %s.\n", t.board)
	if effective {
		text = fmt.Sprintf("Agents may add people to %s.\n", t.board)
	}
	a.emit(struct {
		Board   string `json:"board"`
		Enabled bool   `json:"agents_add_people"`
	}{t.board, effective}, text)
	return nil
}
