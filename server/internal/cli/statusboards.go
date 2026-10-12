package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

type statusBoard struct {
	Name   string             `json:"name"`
	Unread *int               `json:"unread"`
	Policy api.Policy         `json:"policy"`
	Agents []statusBoardAgent `json:"agents"`
}

type statusBoardAgent struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Location *api.AgentLocation `json:"location,omitempty"`
}

func (a *app) personStatusBoards(ctx context.Context, srv serverRef) ([]statusBoard, []addedNotice, error) {
	c, err := a.humanClient(ctx, target{server: srv})
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{})
	if err != nil {
		return nil, nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, nil, apiError(r.StatusCode(), r.Body)
	}
	rows := make([]statusBoard, 0, len(r.JSON200.Boards))
	for _, board := range r.JSON200.Boards {
		if !board.OnBoard || (board.Lifecycle != nil && *board.Lifecycle != api.BoardLifecycleActive) {
			continue
		}
		row := statusBoard{Name: board.Name, Unread: board.Unread, Policy: board.Policy, Agents: []statusBoardAgent{}}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b statusBoard) int { return strings.Compare(a.Name, b.Name) })
	notices := addedFrom(r.JSON200.Boards)
	own, err := a.ownAgentsGroup(ctx, srv, c)
	if err != nil {
		return rows, notices, err
	}
	for i := range rows {
		for _, agent := range own.Agents {
			if agent.Board == rows[i].Name {
				rows[i].Agents = append(rows[i].Agents, statusBoardAgent{ID: agent.ID, Name: agent.Name, Location: agent.Location})
			}
		}
	}
	return rows, notices, nil
}

func statusBoardsText(rows []statusBoard) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Boards: %d\n", len(rows))
	for _, row := range rows {
		unread := "unread unknown"
		if row.Unread != nil {
			unread = fmt.Sprintf("%d unread", *row.Unread)
		}
		policy := string(row.Policy.Preset)
		if policy == "starter" {
			policy += " (a starting point; tighten with aboard board policy recommended)"
		}
		fmt.Fprintf(&text, "  %s · %s · policy %s\n", row.Name, unread, policy)
		for _, agent := range row.Agents {
			fmt.Fprintf(&text, "    @%s", agent.Name)
			if agent.Location == nil {
				text.WriteString(" · location not reported\n")
				continue
			}
			loc := agent.Location
			fmt.Fprintf(&text, " · %q · %q · %q · last active %s\n", deref(loc.Machine), loc.Harness, loc.Folder, loc.LastActive.Format("2006-01-02 15:04:05Z07:00"))
		}
	}
	return text.String()
}
