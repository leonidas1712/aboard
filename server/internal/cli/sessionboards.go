package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// sessionBoards is aboard boards in a session without --as or ABOARD_AGENT: every board
// the session's person can see, listed through the daemon's delegation, with the
// session's seat on each and that seat's unread inbox. The list never authorizes a
// join: join --board checks again.
func (a *app) sessionBoards(ctx context.Context, key delivery.SessionKey, project projectFile, linked, archived bool) error {
	if _, _, err := a.checkSession(ctx); err != nil {
		return err
	}
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return err
	}
	sessionServer, err := sessionIssuer(agents, a.selectedServerFlag())
	if err != nil {
		return err
	}
	srv, err := a.boardServer(ctx, a.selectedServerFlag(), sessionServer)
	if err != nil {
		return err
	}
	lifecycle := ""
	if archived {
		lifecycle = "archived"
	}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpBoards, Harness: key.Harness, Session: key.ID, Server: srv.URL, Lifecycle: lifecycle})
	if err != nil {
		return err
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	rows := []boardsRow{}
	for _, raw := range resp.Boards {
		var b api.Board
		var marked struct {
			Seat *delivery.SeatRef `json:"seat"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return fmt.Errorf("read a listed board: %w", err)
		}
		if err := json.Unmarshal(raw, &marked); err != nil {
			return fmt.Errorf("read a listed board's seat: %w", err)
		}
		row := boardsRow{
			Name: b.Name, Title: b.Title, Visibility: b.Visibility, Lifecycle: b.Lifecycle, OnBoard: b.OnBoard, Agents: b.AgentCount,
			Added:   addedOf(b),
			Default: linked && project.Board == b.Name && (project.Server.URL == "" || project.Server.URL == srv.URL),
			Seat:    &seatName{},
		}
		if row.Added != nil {
			row.Added.Join = "aboard join --server " + commandWord(srv.URL) + " --board " + commandWord(b.Name)
		}
		if b.PeopleCount != nil {
			row.People = *b.PeopleCount
		}
		if marked.Seat != nil {
			row.Seat.name = &marked.Seat.Name
			if cred, ok := seatCredential(creds, srv.URL, marked.Seat.MemberID); ok {
				row.Unread = a.seatUnread(ctx, srv, cred)
			}
		}
		rows = append(rows, row)
	}
	var person *string
	if logins, err := a.readServerLogins(); err == nil {
		if l, ok := logins.find(srv.URL); ok && l.Handle != "" {
			person = &l.Handle
		}
	}
	a.emit(struct {
		Server        serverRef         `json:"server"`
		As            *string           `json:"as"`
		Person        *string           `json:"person,omitempty"`
		Session       string            `json:"session"`
		All           bool              `json:"all"`
		Lifecycle     string            `json:"lifecycle"`
		Boards        []boardsRow       `json:"boards"`
		ArchivedCount *int              `json:"archived_count,omitempty"`
		Hidden        []api.HiddenBoard `json:"hidden_boards"`
	}{srv, nil, person, key.String(), false, listLifecycle(archived), rows, resp.ArchivedCount, []api.HiddenBoard{}},
		strings.ReplaceAll(sessionBoardsText(rows, archived), "join with aboard join --board ", "join with aboard join --server "+commandWord(srv.URL)+" --board ")+strings.ReplaceAll(archivedHint(archived, false, resp.ArchivedCount), "aboard boards --archived", "aboard boards --archived --server "+commandWord(srv.URL)))
	return nil
}

// seatUnread is a seat's unread inbox, read with its own token; nil when it can't be read.
func (a *app) seatUnread(ctx context.Context, srv serverRef, cred agentCredential) *int {
	c, err := a.client(ctx, srv, cred.Token, requestTimeout)
	if err != nil {
		return nil
	}
	all := api.ListBoardsParamsLifecycleAll
	r, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{Lifecycle: &all})
	if err != nil || r.JSON200 == nil {
		return nil
	}
	for _, b := range r.JSON200.Boards {
		if b.Name == cred.Board {
			return b.Unread
		}
	}
	return nil
}

// sessionBoardsText is aboard boards' text output in a session: one line per board,
// with its visibility, its agents, and the session's seat or the person's place on it.
func sessionBoardsText(rows []boardsRow, archived bool) string {
	if len(rows) == 0 && archived {
		return "No archived boards you can see.\n"
	}
	if len(rows) == 0 {
		return "No boards you can see yet; a person makes one with aboard pair or in the board view.\n"
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Name))
	}
	var b strings.Builder
	for _, r := range rows {
		cols := []string{fmt.Sprintf("%-*s", width, r.Name), fmt.Sprintf("%-7s", string(r.Visibility))}
		if r.Agents != nil && *r.Agents > 0 {
			cols = append(cols, fmt.Sprintf("%-8s", counted(*r.Agents, "agent")))
		}
		switch {
		case r.Seat != nil && r.Seat.name != nil:
			cols = append(cols, "you're "+*r.Seat.name+" here")
		case r.OnBoard && r.Added != nil:
			cols = append(cols, "new, added by "+r.Added.by()+"; join with "+r.Added.Join)
		case r.OnBoard:
			cols = append(cols, "you're on it")
		case archived:
			cols = append(cols, "archived")
		default:
			cols = append(cols, "join with aboard join --board "+r.Name)
		}
		b.WriteString(strings.Join(cols, "   ") + "\n")
	}
	return b.String()
}
