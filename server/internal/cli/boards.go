package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// boardsRow is one board in aboard boards' --json output.
type boardsRow struct {
	Name       string              `json:"name"`
	Title      *string             `json:"title"`
	Visibility api.BoardVisibility `json:"visibility"`
	// Lifecycle is the board's lifecycle as the server gave it; absent means active.
	Lifecycle *api.BoardLifecycle `json:"lifecycle,omitempty"`
	OnBoard   bool                `json:"on_board"`
	Role      *api.BoardRole      `json:"role"`
	People    int                 `json:"people"`
	Agents    *int                `json:"agents"`
	Unread    *int                `json:"unread"`
	Default   bool                `json:"default"`
	// Seat is, when listing through the machine's delegation from a session, the
	// session's seat on the board (its agent's name) or nil.
	Seat *seatName `json:"seat,omitempty"`
	// Added is set while someone else's add of the person is new to them.
	Added *addedNotice `json:"added,omitempty"`
}

// seatName wraps a seat's name so a row can say "no seat" (null) apart from leaving
// the field out.
type seatName struct{ name *string }

func (s seatName) MarshalJSON() ([]byte, error) { return json.Marshal(s.name) }

// runBoards lists the boards the person is on, or with --all every board they can see.
// When an agent is selected (--as, ABOARD_AGENT or a harness session), it lists only the agent's own board, with the
// agent's token: it never reads with the person's login there.
func runBoards(ctx context.Context, a *app, args []string) error {
	use := usageOf("boards")
	fs := a.flags("boards")
	all := fs.Bool("all", false, "also list open boards you aren't on, and for an admin, private boards you aren't on")
	archived := fs.Bool("archived", false, "list only archived boards")
	as := fs.String("as", "", "list this agent's board")
	serverFlag := fs.String("server", "", "list boards on this server only")
	allServers := fs.Bool("all-servers", false, "list boards on every known server")
	boardFlag := fs.String("board", "", "the agent's board, when its name is used on several")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if *allServers && (*serverFlag != "" || a.agentSelected(*as)) {
		return usageError("--all-servers is person-only and cannot be combined with --server.", use)
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
	if key, inSession := a.sessionKey(); inSession && *as == "" && strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")) == "" {
		if *boardFlag != "" {
			return usageError("--board picks an agent's board, so it works only with --as.", use)
		}
		return a.sessionBoards(ctx, key, project, linked, *archived)
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
		return a.humanBoards(ctx, project, linked, *serverFlag, *all, *archived, *allServers)
	}
	g, err := a.boardsGroup(ctx, srv, c, agent, project, linked, *all, *archived)
	if err != nil {
		return err
	}
	a.emit(g, boardsText(a.out(), srv, agent, *all, *archived, g.Boards, g.Hidden)+archivedHint(*archived, *all, g.ArchivedCount))
	return nil
}

func (a *app) boardsGroup(ctx context.Context, srv serverRef, c *client, agent *string, project projectFile, linked, all, archived bool) (boardsOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	params := &api.ListBoardsParams{All: &all}
	if archived {
		l := api.ListBoardsParamsLifecycleArchived
		params.Lifecycle = &l
	}
	r, err := c.api.ListBoardsWithResponse(ctx, params)
	if err != nil {
		return boardsOutput{}, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return boardsOutput{}, apiError(r.StatusCode(), r.Body)
	}
	me := ""
	if agent == nil {
		m, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return boardsOutput{}, c.unreachable(err)
		}
		if m.JSON200 == nil {
			return boardsOutput{}, apiError(m.StatusCode(), m.Body)
		}
		me = m.JSON200.Name
	}
	rows := []boardsRow{}
	for _, b := range r.JSON200.Boards {
		row := boardsRow{
			Name: b.Name, Title: b.Title, Visibility: b.Visibility, Lifecycle: b.Lifecycle, OnBoard: b.OnBoard, Unread: b.Unread,
			Added:   addedOf(b),
			Default: linked && project.Board == b.Name && (project.Server.URL == "" || project.Server.URL == srv.URL),
		}
		if row.Added != nil {
			row.Added.Join = "aboard join --board " + commandWord(b.Name) + " --server " + commandWord(srv.URL)
		}
		p, err := c.api.ListPeopleWithResponse(ctx, b.Name)
		if err != nil {
			return boardsOutput{}, c.unreachable(err)
		}
		if p.JSON200 == nil {
			return boardsOutput{}, apiError(p.StatusCode(), p.Body)
		}
		row.People = len(p.JSON200.People)
		for _, person := range p.JSON200.People {
			if agent == nil && person.Handle == me {
				role := person.BoardRole
				row.Role = &role
			}
		}
		if b.OnBoard {
			m, err := c.api.ListMembersWithResponse(ctx, b.Name, nil)
			if err != nil {
				return boardsOutput{}, c.unreachable(err)
			}
			if m.JSON200 == nil {
				return boardsOutput{}, apiError(m.StatusCode(), m.Body)
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
	return boardsOutput{Server: srv, As: agent, All: all, Lifecycle: listLifecycle(archived), Boards: rows, ArchivedCount: r.JSON200.ArchivedCount, Hidden: hidden}, nil
}

// listLifecycle is the lifecycle aboard boards lists, for its --json output.
func listLifecycle(archived bool) string {
	if archived {
		return "archived"
	}
	return "active"
}

// archivedHint is the one quiet line under a list of active boards when some boards in
// the same scope are archived, saying how to list them.
func archivedHint(archived, all bool, count *int) string {
	if archived || count == nil || *count == 0 {
		return ""
	}
	command := "aboard boards --archived"
	if all {
		command += " --all"
	}
	return counted(*count, "archived board") + ": " + command + "\n"
}

// boardsText is aboard boards' text output.
func boardsText(st styles, srv serverRef, agent *string, all, archived bool, rows []boardsRow, hidden []api.HiddenBoard) string {
	var b strings.Builder
	heading := func(format string, args ...any) {
		b.WriteString(st.heading(strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")) + "\n")
	}
	switch {
	case agent != nil && archived:
		heading("Archived boards of agent %s on %s: an agent sees only its own board.\n", *agent, srv.URL)
	case agent != nil:
		heading("Boards of agent %s on %s: an agent sees only its own board.\n", *agent, srv.URL)
	case all && archived:
		heading("Archived boards you can see on %s:\n", srv.URL)
	case all:
		heading("Boards you can see on %s:\n", srv.URL)
	case archived:
		heading("Your archived boards on %s:\n", srv.URL)
	default:
		heading("Your boards on %s:\n", srv.URL)
	}
	switch {
	case len(rows) == 0 && archived:
		b.WriteString("  none\n")
	case len(rows) == 0:
		b.WriteString("  none yet; run aboard pair to make one\n")
	}
	for _, r := range rows {
		parts := []string{st.name(r.Name)}
		if r.Title != nil {
			parts[0] += fmt.Sprintf(" %q", *r.Title)
		}
		// Open says something only once other people can be on the board, as in the board view.
		if r.Visibility == api.BoardVisibilityPrivate || r.People > 1 {
			parts = append(parts, map[api.BoardVisibility]string{api.BoardVisibilityOpen: "open", api.BoardVisibilityPrivate: st.warn("private")}[r.Visibility])
		}
		switch {
		case !r.OnBoard:
			parts = append(parts, st.warn("not joined"))
		case r.Role != nil:
			parts = append(parts, string(*r.Role))
		}
		parts = append(parts, st.dim(peopleCount(r.People)))
		if r.Agents != nil {
			parts = append(parts, st.dim(counted(*r.Agents, "agent")))
		}
		if r.Unread != nil && *r.Unread > 0 {
			parts = append(parts, st.warn(fmt.Sprintf("%d unread", *r.Unread)))
		}
		if r.Added != nil {
			parts = append(parts, "new, added by "+r.Added.by())
		}
		if r.Default {
			parts = append(parts, st.ok("default"))
		}
		line := "  " + strings.Join(parts, " · ")
		if !r.OnBoard && !archived {
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

// boardsOutput keeps the single-server result while adding groups for a machine-wide list.
type boardsOutput struct {
	Server        serverRef         `json:"server"`
	As            *string           `json:"as"`
	All           bool              `json:"all"`
	Lifecycle     string            `json:"lifecycle"`
	Boards        []boardsRow       `json:"boards"`
	ArchivedCount *int              `json:"archived_count,omitempty"`
	Hidden        []api.HiddenBoard `json:"hidden_boards"`
	Error         *boardsFailure    `json:"error,omitempty"`
	Servers       []boardsOutput    `json:"servers,omitempty"`
}

type boardsFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (a *app) humanBoards(ctx context.Context, project projectFile, linked bool, flag string, all, archived, allServers bool) error {
	known, def, err := a.knownServers()
	if err != nil {
		return err
	}
	if !allServers {
		srv, err := a.resolveServer(flag)
		if err != nil {
			return err
		}
		known = []knownServer{{Name: srv.Name, URL: srv.URL}}
	} else if len(known) == 0 {
		return serverNotSelected(known)
	}
	groups := make([]boardsOutput, 0, len(known))
	var text strings.Builder
	var firstError error
	successes, primary := 0, 0
	for i, k := range known {
		srv := k.ref()
		g, err := a.humanBoardsGroup(ctx, srv, project, linked, all, archived)
		if err != nil {
			if len(known) == 1 {
				return err
			}
			if firstError == nil {
				firstError = err
			}
			e := asError(err)
			g = boardsOutput{Server: srv, All: all, Lifecycle: listLifecycle(archived), Boards: []boardsRow{}, Hidden: []api.HiddenBoard{}, Error: &boardsFailure{Code: e.Code, Message: e.Message, Hint: e.Hint}}
			bst := a.out()
			fmt.Fprintf(&text, "%s %s %s\n%s %s\n", bst.heading("Boards on "+srv.URL+":"), bst.bad("unavailable"), "("+e.Code+").", bst.warn("Hint:"), e.Hint)
		} else {
			successes++
			groupText := boardsText(a.out(), srv, nil, all, archived, g.Boards, g.Hidden)
			if len(known) > 1 || flag != "" {
				groupText = strings.ReplaceAll(groupText, "join with aboard board add @me --board ", "join with aboard board add @me --server "+commandWord(srv.URL)+" --board ")
			}
			text.WriteString(groupText)
			hint := archivedHint(archived, all, g.ArchivedCount)
			if len(known) > 1 || flag != "" {
				hint = strings.ReplaceAll(hint, "aboard boards --archived", "aboard boards --archived --server "+commandWord(srv.URL))
			}
			text.WriteString(hint)
		}
		if def != nil && def.URL == srv.URL {
			primary = i
		}
		groups = append(groups, g)
	}
	if linked {
		for i, g := range groups {
			if g.Server.URL == project.Server.URL {
				primary = i
			}
		}
	}
	out := groups[primary]
	if len(groups) > 1 {
		out.Servers = groups
	}
	a.emit(out, text.String())
	if successes == 0 && firstError != nil {
		return errReportedFailure
	}
	return nil
}

func (a *app) humanBoardsGroup(ctx context.Context, srv serverRef, project projectFile, linked, all, archived bool) (boardsOutput, error) {
	if srv.URL == a.localServer().URL {
		if _, err := a.ensureLocal(ctx); err != nil {
			return boardsOutput{}, err
		}
	}
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return boardsOutput{}, err
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return boardsOutput{}, err
	}
	return a.boardsGroup(ctx, srv, c, nil, project, linked, all, archived)
}
