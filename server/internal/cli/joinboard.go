package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// A session finds and joins its person's boards by name through the delivery daemon,
// which holds this machine's delegation for the server (spec/control.md, "boards" and
// "join"); the command never reads the person's key for that. In a terminal, join
// --board adds the person themselves, with their own key.

// sessionPattern is what the API accepts as a session (`<harness>:<id>`).
var sessionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}:[A-Za-z0-9._-]{1,200}$`)

// sessionParam is the session a join made from inside it sends with a person's key, so
// a later join from the same session through the delegation finds the seat; nil outside
// a session, or for a session id the API wouldn't take.
func sessionParam(key delivery.SessionKey, inSession bool) *string {
	if !inSession || !sessionPattern.MatchString(key.String()) {
		return nil
	}
	s := key.String()
	return &s
}

// boardServer is the server join --board and boards act on: the server of the session's
// seats, else --server, else the one this directory's .aboard names, else this
// machine's default server, else the one server this machine is connected to, else the local server. A machine connected to
// several names one with --server.
func (a *app) boardServer(ctx context.Context, flag, sessionServer string) (serverRef, error) {
	if sessionServer != "" && flag == "" {
		return a.serverRefFor(sessionServer), nil
	}
	if flag == "" {
		p, ok, err := a.readProject()
		if err != nil {
			return serverRef{}, err
		}
		if !ok || p.Server.URL == "" {
			logins, err := a.readServerLogins()
			if err != nil {
				return serverRef{}, err
			}
			_, def, err := a.knownServers()
			if err != nil {
				return serverRef{}, err
			}
			switch {
			case def != nil:
				flag = def.URL
			case len(logins.Servers) == 0:
			case len(logins.Servers) == 1:
				flag = logins.Servers[0].URL
			default:
				choices := make([]string, 0, len(logins.Servers))
				for _, s := range logins.Servers {
					choices = append(choices, s.URL)
				}
				e := newError("server_not_selected", "This machine is connected to several servers, and the board is on one of them.",
					"Name its server with --server, such as --server "+choices[0]+".")
				e.Details = map[string]any{"choices": choices}
				return serverRef{}, e
			}
		}
	}
	srv, _, err := a.personServer(ctx, flag)
	return srv, err
}

// runJoinBoard is aboard join --board: in a session it gives the session a seat on the
// board through the daemon; in a terminal it adds the person to the board.
func runJoinBoard(ctx context.Context, a *app, board, role, name, serverFlag string) error {
	key, inSession := a.sessionKey()
	if !inSession {
		if a.env.Getenv("ABOARD_AGENT") != "" {
			return newError("session_unknown",
				"aboard join --board gives a session a seat, and this command isn't running in a session; ABOARD_AGENT names an agent, not a session.",
				"Run it inside the session that should join, or unset ABOARD_AGENT to add yourself to the board.")
		}
		return a.joinBoardAsPerson(ctx, board, serverFlag)
	}
	if _, _, err := a.checkSession(ctx); err != nil {
		return err
	}
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return err
	}
	sessionServer := ""
	if len(agents) > 0 {
		sessionServer = agents[0].Server
	}
	srv, err := a.boardServer(ctx, serverFlag, sessionServer)
	if err != nil {
		return err
	}
	resp, err := a.callDaemon(ctx, delivery.Request{
		Op: delivery.OpJoin, Harness: key.Harness, Session: key.ID, Role: role,
		Agent: &delivery.AgentRef{Server: srv.URL, Board: board, Name: name},
	})
	if err != nil {
		return err
	}
	if resp.Joined == nil {
		return newError("internal", "The delivery daemon answered the join without a seat.", "Run aboard down, then the join again.")
	}
	var b api.Board
	var agent api.Member
	if err := json.Unmarshal(resp.Board, &b); err != nil {
		return fmt.Errorf("read the joined board: %w", err)
	}
	if err := json.Unmarshal(resp.Member, &agent); err != nil {
		return fmt.Errorf("read the joined seat: %w", err)
	}
	var moved *previousAgent
	if resp.Previous != nil {
		moved = &previousAgent{Name: resp.Previous.Name, Board: resp.Previous.Board}
	}
	previous, err := a.linkProject(projectFile{Server: srv, Board: b.Name})
	if err != nil {
		return err
	}
	roleCharter := ""
	if r, ok := b.Roles[deref(agent.Role)]; ok {
		roleCharter = deref(r.Charter)
	}
	useAs := useFor(agent.Name)
	useAs.BoundSession = optional(key.String())
	mode := a.deliveryFor(ctx, delivery.AgentRef{Server: srv.URL, Board: b.Name, Name: agent.Name, MemberID: agent.Id}, string(resp.Mode))
	first := fmt.Sprintf("Joined board %s as %s\n", b.Name, agentText(agent))
	if resp.Reused {
		first = fmt.Sprintf("This session is already %s on %s.\n", agent.Name, b.Name)
	}
	text := first + fmt.Sprintf("This session acts as %s on %s, and messages for %s arrive here.\n", agent.Name, b.Name, agent.Name) +
		movedText(moved, agent.Name, b.Name) + relinkedText(b.Name, previous) + mode.line() + a.seatBoardReminder(ctx, key, b.Name) + briefJoinHint(b)
	via := "delegation"
	a.emit(struct {
		Via           string         `json:"via"`
		Reused        bool           `json:"reused"`
		Server        serverRef      `json:"server"`
		Board         api.Board      `json:"board"`
		Agent         api.Member     `json:"agent"`
		Use           agentUse       `json:"use"`
		Charter       string         `json:"charter"`
		RoleCharter   string         `json:"role_charter"`
		PolicyNotice  *policyNotice  `json:"policy_notice"`
		PreviousBoard *string        `json:"previous_board"`
		PreviousAgent *previousAgent `json:"previous_agent"`
		seatDelivery
	}{via, resp.Reused, srv, b, agent, useAs, b.Charter, roleCharter, noticeFor(b.Policy), optional(previous), moved, mode}, text)
	return nil
}

// joinBoardAsPerson adds the person to an open board with their own key, as joining it
// from the board view does; on a board they are on it changes nothing.
func (a *app) joinBoardAsPerson(ctx context.Context, board, serverFlag string) error {
	srv, err := a.boardServer(ctx, serverFlag, "")
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	me, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if me.JSON200 == nil {
		return keyRejected(srv, me.StatusCode(), me.Body)
	}
	added := true
	r, err := c.api.AddPersonWithResponse(ctx, board, &api.AddPersonParams{}, api.AddPersonRequest{Handle: me.JSON200.Name})
	if err != nil {
		return c.unreachable(err)
	}
	var person *api.BoardPerson
	switch {
	case r.JSON201 != nil:
		person = r.JSON201
	case r.JSON409 != nil && r.JSON409.Error.Code == "already_on_board":
		added = false
		people, err := c.api.ListPeopleWithResponse(ctx, board)
		if err != nil {
			return c.unreachable(err)
		}
		if people.JSON200 == nil {
			return apiError(people.StatusCode(), people.Body)
		}
		for i := range people.JSON200.People {
			if people.JSON200.People[i].Handle == me.JSON200.Name {
				person = &people.JSON200.People[i]
			}
		}
		if person == nil {
			return apiError(r.StatusCode(), r.Body)
		}
	default:
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(struct {
		Server serverRef       `json:"server"`
		Board  string          `json:"board"`
		Person api.BoardPerson `json:"person"`
		Added  bool            `json:"added"`
	}{srv, board, *person, added},
		fmt.Sprintf("You're on %s. Post with aboard say --board %s \"…\", or in the board view.\n", board, board))
	return nil
}

// boardAmbiguous refuses a command that acts on one board, run without --board in a
// session with several seats: it names the session's working seats and the flag. It is
// checked before agent_ambiguous.
func boardAmbiguous(seats []delivery.AgentRef, boardFlag string) error {
	if boardFlag != "" || len(seats) < 2 {
		return nil
	}
	sorted := slices.Clone(seats)
	slices.SortFunc(sorted, func(x, y delivery.AgentRef) int { return strings.Compare(x.Board, y.Board) })
	boards := make([]string, 0, len(sorted))
	list := make([]map[string]string, 0, len(sorted))
	for _, s := range sorted {
		boards = append(boards, s.Board)
		list = append(list, map[string]string{"board": s.Board, "name": s.Name})
	}
	e := newError("board_ambiguous",
		fmt.Sprintf("This session has seats on %d boards: %s.", len(boards), strings.Join(boards, ", ")),
		fmt.Sprintf("Pass --board with one of them, such as aboard say --board %s \"…\".", boards[0]))
	e.Details = map[string]any{"boards": boards, "seats": list}
	return e
}
