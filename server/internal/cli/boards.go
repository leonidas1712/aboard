package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// boardsRow is one board in aboard boards' --json output.
type boardsRow struct {
	Name       string              `json:"name"`
	Title      *string             `json:"title"`
	Visibility api.BoardVisibility `json:"visibility"`
	OnBoard    bool                `json:"on_board"`
	Role       *api.BoardRole      `json:"role"`
	People     int                 `json:"people"`
	Agents     *int                `json:"agents"`
	Unread     *int                `json:"unread"`
	Default    bool                `json:"default"`
}

// runBoards lists the boards the person is on, or with --all every board they can see.
// When an agent is selected (--as, ABOARD_AGENT or a harness session), it lists only the agent's own board, with the
// agent's token: it never reads with the person's login there.
func runBoards(ctx context.Context, a *app, args []string) error {
	use := usageOf("boards")
	fs := a.flags("boards")
	all := fs.Bool("all", false, "also list open boards you aren't on, and for an admin, private boards you aren't on")
	as := fs.String("as", "", "list this agent's board")
	boardFlag := fs.String("board", "", "the agent's board, when its name is used on several")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	var (
		srv   serverRef
		c     *client
		agent *string
		err   error
	)
	project, linked, err := a.readProject()
	if err != nil {
		return err
	}
	if a.agentSelected(*as) {
		if *all {
			return usageError("An agent sees only its own board, so --all works only for a person: without --as or ABOARD_AGENT, outside an agent's session.", use)
		}
		t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
		if err != nil {
			return err
		}
		srv, agent = t.server, &cred.Name
		if c, err = a.client(ctx, srv, cred.Token, requestTimeout); err != nil {
			return err
		}
	} else {
		if *boardFlag != "" {
			return usageError("--board picks an agent's board, so it works only with --as.", use)
		}
		srv = a.localServer()
		if linked && project.Server.URL != "" {
			srv = project.Server
		}
		if srv.URL == a.localServer().URL {
			if _, err := a.ensureLocal(ctx); err != nil {
				return err
			}
		}
		token, err := a.readOwnerToken(srv)
		if err != nil {
			return err
		}
		if c, err = a.client(ctx, srv, token, requestTimeout); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: all})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	me := ""
	if agent == nil {
		m, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if m.JSON200 == nil {
			return apiError(m.StatusCode(), m.Body)
		}
		me = m.JSON200.Name
	}
	rows := []boardsRow{}
	for _, b := range r.JSON200.Boards {
		row := boardsRow{
			Name: b.Name, Title: b.Title, Visibility: b.Visibility, OnBoard: b.OnBoard, Unread: b.Unread,
			Default: linked && project.Board == b.Name && (project.Server.URL == "" || project.Server.URL == srv.URL),
		}
		p, err := c.api.ListPeopleWithResponse(ctx, b.Name)
		if err != nil {
			return c.unreachable(err)
		}
		if p.JSON200 == nil {
			return apiError(p.StatusCode(), p.Body)
		}
		row.People = len(p.JSON200.People)
		for _, person := range p.JSON200.People {
			if agent == nil && person.Handle == me {
				role := person.BoardRole
				row.Role = &role
			}
		}
		if b.OnBoard {
			m, err := c.api.ListMembersWithResponse(ctx, b.Name)
			if err != nil {
				return c.unreachable(err)
			}
			if m.JSON200 == nil {
				return apiError(m.StatusCode(), m.Body)
			}
			n := 0
			for _, x := range m.JSON200.Members {
				if x.Kind == api.MemberKindAgent {
					n++
				}
			}
			row.Agents = &n
		}
		rows = append(rows, row)
	}
	hidden := []api.HiddenBoard{}
	if r.JSON200.HiddenBoards != nil {
		hidden = *r.JSON200.HiddenBoards
	}
	a.emit(struct {
		Server serverRef         `json:"server"`
		As     *string           `json:"as"`
		All    bool              `json:"all"`
		Boards []boardsRow       `json:"boards"`
		Hidden []api.HiddenBoard `json:"hidden_boards"`
	}{srv, agent, *all, rows, hidden}, boardsText(srv, agent, *all, rows, hidden))
	return nil
}

// boardsText is aboard boards' text output.
func boardsText(srv serverRef, agent *string, all bool, rows []boardsRow, hidden []api.HiddenBoard) string {
	var b strings.Builder
	switch {
	case agent != nil:
		fmt.Fprintf(&b, "Boards of agent %s on %s: an agent sees only its own board.\n", *agent, srv.URL)
	case all:
		fmt.Fprintf(&b, "Boards you can see on %s:\n", srv.URL)
	default:
		fmt.Fprintf(&b, "Your boards on %s:\n", srv.URL)
	}
	if len(rows) == 0 {
		b.WriteString("  none yet; run aboard pair to make one\n")
	}
	for _, r := range rows {
		parts := []string{r.Name}
		if r.Title != nil {
			parts[0] += fmt.Sprintf(" %q", *r.Title)
		}
		// Open says something only once other people can be on the board, as in the board view.
		if r.Visibility == api.BoardVisibilityPrivate || r.People > 1 {
			parts = append(parts, map[api.BoardVisibility]string{api.BoardVisibilityOpen: "open", api.BoardVisibilityPrivate: "private"}[r.Visibility])
		}
		switch {
		case !r.OnBoard:
			parts = append(parts, "not joined")
		case r.Role != nil:
			parts = append(parts, string(*r.Role))
		}
		parts = append(parts, peopleCount(r.People))
		if r.Agents != nil {
			parts = append(parts, counted(*r.Agents, "agent"))
		}
		if r.Unread != nil && *r.Unread > 0 {
			parts = append(parts, fmt.Sprintf("%d unread", *r.Unread))
		}
		if r.Default {
			parts = append(parts, "default")
		}
		line := "  " + strings.Join(parts, " · ")
		if !r.OnBoard {
			line += "; join with aboard board add @me --board " + r.Name
		}
		b.WriteString(line + "\n")
	}
	if len(hidden) > 0 {
		b.WriteString("Private boards you aren't on (as an admin you see only that they exist):\n")
		for _, h := range hidden {
			fmt.Fprintf(&b, "  %s · created by %s on %s · %s\n", h.Id, h.CreatedBy.Handle, h.CreatedAt.Format("2006-01-02"), peopleCount(h.People))
		}
	}
	return b.String()
}

// peopleCount says how many people: "1 person", "3 people".
func peopleCount(n int) string { return fmt.Sprintf("%d %s", n, plural(n, "person", "people")) }
