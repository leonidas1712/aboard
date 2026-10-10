package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// staysLinked is the board on another server a directory stays linked to.
type staysLinked struct {
	Server serverRef `json:"server"`
	Board  string    `json:"board"`
}

// runBoardNew creates a board as a person in a terminal, or creates and binds its
// agent seat atomically through the daemon in a harness session.
func runBoardNew(ctx context.Context, a *app, name, title string, private bool, serverFlag string) error {
	if key, inSession := a.sessionKey(); inSession {
		return a.runSessionBoardNew(ctx, key, name, title, private, serverFlag)
	}
	if a.agentSelected("") {
		return creationNeedsSession()
	}
	// The command handed to the person keeps every flag given, quoted for a shell.
	command := "aboard board new " + commandWord(name)
	if title != "" {
		command += " --title " + commandWord(title)
	}
	if private {
		command += " --private"
	}
	if serverFlag != "" {
		command += " --server " + commandWord(serverFlag)
	}
	if err := a.refuseInSession("Creating a board", command); err != nil {
		return err
	}
	srv, _, err := a.bootstrapServer(ctx, serverFlag)
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
	linked := false
	var stays *staysLinked
	join := "aboard join --server " + commandWord(srv.URL) + " --board " + commandWord(b.Name)
	text := fmt.Sprintf("Created board %s on %s, open to everyone on the server.\n", b.Name, srv.URL)
	if b.Visibility == api.BoardVisibilityPrivate {
		text = fmt.Sprintf("Created board %s on %s, private: only its people see it.\n", b.Name, srv.URL)
	}
	add := "aboard board add @handle --server " + commandWord(srv.URL) + " --board " + commandWord(b.Name)
	notice := noticeFor(b.Policy)
	if notice != nil {
		text += notice.Message + "\n"
	}
	text += fmt.Sprintf("Next: from an agent's session, run %s; to bring a person onto it, %s\n", join, add)
	a.emit(map[string]any{"server": srv, "board": b, "linked": linked, "stays_linked": stays, "policy_notice": notice, "join_command": join}, text)
	return nil
}
