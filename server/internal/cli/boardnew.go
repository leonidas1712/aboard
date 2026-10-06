package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// runBoardNew creates a board as the person, with their own key, on the server
// personServer picks, and says how agents and people get onto it. No agent joins. A
// directory linked to no board is linked to it. It is up to a person, so it refuses
// inside a harness session.
func runBoardNew(ctx context.Context, a *app, name, title string, private bool, serverFlag string) error {
	command := "aboard board new " + name
	if err := a.refuseInSession("Creating a board", command); err != nil {
		return err
	}
	srv, _, err := a.personServer(ctx, serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	template := "general"
	vis := api.BoardVisibilityOpen
	if private {
		vis = api.BoardVisibilityPrivate
	}
	r, err := c.api.CreateBoardWithResponse(ctx, &api.CreateBoardParams{}, api.CreateBoardRequest{
		Name: &name, Title: optional(strings.TrimSpace(title)), Template: &template, Visibility: &vis,
	})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	b := r.JSON201
	// A directory linked to no board is linked to this one, as pair does, so the board
	// commands that follow, run here, act on it. A linked directory keeps its board.
	_, linked, err := a.readProject()
	if err != nil {
		return err
	}
	linked = !linked
	if linked {
		if err := a.writeProject(projectFile{Server: srv, Board: b.Name}); err != nil {
			return err
		}
	}
	join := "aboard join --board " + b.Name
	text := fmt.Sprintf("Created board %s on %s, open to everyone on the server.\n", b.Name, srv.URL)
	if b.Visibility == api.BoardVisibilityPrivate {
		text = fmt.Sprintf("Created board %s on %s, private: only the people on it see it.\n", b.Name, srv.URL)
	}
	add := "aboard board add @handle"
	if linked {
		text += "Linked this directory to " + b.Name + ", so board commands run here act on it.\n"
	} else {
		add += " --board " + b.Name
	}
	notice := noticeFor(b.Policy)
	if notice != nil {
		text += notice.Message + "\n"
	}
	text += fmt.Sprintf("Next: from an agent's session, run %s; to bring a person onto it, %s\n", join, add)
	a.emit(map[string]any{"server": srv, "board": b, "linked": linked, "policy_notice": notice, "join_command": join}, text)
	return nil
}
