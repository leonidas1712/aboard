package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/boardfile"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/joinline"
)

// defaultTemplate is the board template pair uses when none is named.
const defaultTemplate = "writer-reviewer"

// runPair creates a board from a template, joins this session as the template's first
// role and prints a join line for a second session in the other role.
func runPair(ctx context.Context, a *app, args []string) error {
	const use = "aboard pair [template] [--new] [--board NAME] [--name NAME] [--json]"
	fs := a.flags("pair")
	boardName := fs.String("board", "", "name of the new board")
	agentName := fs.String("name", "", "name of this session's agent")
	newBoard := fs.Bool("new", false, "create another board even though this directory is linked to one")
	pos, err := a.parse(fs, args, use, 0, 1)
	if err != nil {
		return err
	}
	if err := a.refuseIfLinked(*newBoard); err != nil {
		return err
	}
	tmpl := defaultTemplate
	if len(pos) == 1 {
		tmpl = pos[0]
	}
	f, err := boardfile.Template(tmpl)
	if errors.Is(err, boardfile.ErrNoTemplate) {
		return &Error{
			Code: "template_not_found", Message: fmt.Sprintf("There is no template called %q.", tmpl),
			Hint: "Use one of: " + strings.Join(boardfile.TemplateNames(), ", ") + ".", Err: err,
		}
	}
	if err != nil {
		return fmt.Errorf("load template %s: %w", tmpl, err)
	}
	if len(f.Pair) != 2 {
		return newError("invalid_request", fmt.Sprintf("The template %q doesn't name two roles to pair.", tmpl),
			"Use a template made for pairing, such as "+defaultTemplate+".")
	}

	session, inSession, err := a.checkSession(ctx)
	if err != nil {
		return err
	}
	started, err := a.ensureLocal(ctx)
	if err != nil {
		return err
	}
	srv := a.localServer()
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	created, err := c.api.CreateBoardWithResponse(ctx, &api.CreateBoardParams{},
		api.CreateBoardRequest{Template: &tmpl, Name: optional(*boardName)})
	if err != nil {
		return c.unreachable(err)
	}
	if created.JSON201 == nil {
		return apiError(created.StatusCode(), created.Body)
	}
	board := created.JSON201.Name

	joined, err := c.join(ctx, api.JoinRequest{Board: &board, Role: &f.Pair[0], Name: optional(*agentName), Harness: harnessOf(session, inSession, "")})
	if err != nil {
		return err
	}
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: board, Name: joined.Agent.Name, Token: joined.Token}); err != nil {
		return err
	}
	useAs := useFor(joined.Agent.Name)
	if inSession {
		if err := a.bindSession(ctx, session, delivery.AgentRef{Server: srv.URL, Board: board, Name: joined.Agent.Name}); err != nil {
			return err
		}
		useAs.BoundSession = optional(session.String())
	}

	code, err := c.api.CreateJoinCodeWithResponse(ctx, board, &api.CreateJoinCodeParams{}, api.CreateJoinCodeRequest{Role: f.Pair[1]})
	if err != nil {
		return c.unreachable(err)
	}
	if code.JSON201 == nil {
		return apiError(code.StatusCode(), code.Body)
	}
	jc := code.JSON201
	line := deref(jc.JoinLine)
	if line == "" {
		line = joinline.Line{Board: board, Server: "localhost", Role: jc.Role, Code: deref(jc.Code)}.String()
	}

	previous, err := a.linkProject(projectFile{Server: srv, Board: board})
	if err != nil {
		return err
	}

	notice := noticeFor(joined.Board.Policy)
	var text strings.Builder
	if started {
		text.WriteString("Started local Aboard at " + srv.URL + "\n")
	} else {
		text.WriteString("Using local Aboard at " + srv.URL + "\n")
	}
	fmt.Fprintf(&text, "Created board %s and joined as %s (owner %s)\n", board, joined.Agent.Name, deref(joined.Agent.Owner))
	text.WriteString(relinkedText(board, previous))
	if notice != nil {
		text.WriteString(notice.Message + "\n")
	}
	text.WriteString("\nPaste this into your next session:\n" + line + "\n")

	a.emit(struct {
		Server        serverRef     `json:"server"`
		ServerStarted bool          `json:"server_started"`
		Board         api.Board     `json:"board"`
		Agent         api.Member    `json:"agent"`
		Use           agentUse      `json:"use"`
		Join          pairJoin      `json:"join"`
		PolicyNotice  *policyNotice `json:"policy_notice"`
		PreviousBoard *string       `json:"previous_board"`
	}{
		srv, started, joined.Board, joined.Agent, useAs,
		pairJoin{Code: deref(jc.Code), Line: line, Role: jc.Role, ExpiresAt: jc.ExpiresAt},
		notice, optional(previous),
	}, text.String())
	return nil
}

// pairJoin is the join code and line pair prints for the next session.
type pairJoin struct {
	Code      string    `json:"code"`
	Line      string    `json:"line"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

// join makes the caller's human login create an agent on a board.
func (c *client) join(ctx context.Context, req api.JoinRequest) (*api.JoinResult, error) {
	r, err := c.api.JoinWithResponse(ctx, &api.JoinParams{}, req)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON201 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON201, nil
}

// harnessOf is the harness recorded for a new agent: the flag, else the session's.
func harnessOf(session delivery.SessionKey, inSession bool, flag string) *string {
	if flag == "" && inSession {
		flag = session.Harness
	}
	return optional(flag)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// runJoin redeems a join line or a bare join code, creating an agent for this machine.
func runJoin(ctx context.Context, a *app, args []string) error {
	const use = "aboard join <join-line|code> [--name NAME] [--harness H] [--json]"
	fs := a.flags("join")
	agentName := fs.String("name", "", "name of the new agent")
	harness := fs.String("harness", "", "the program running this session, such as claude-code or codex")
	pos, err := a.parse(fs, args, use, 1, -1)
	if err != nil {
		return err
	}
	srv, code, err := a.joinTarget(strings.Join(pos, " "))
	if err != nil {
		return err
	}
	session, inSession, err := a.checkSession(ctx)
	if err != nil {
		return err
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
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	joined, err := c.join(ctx, api.JoinRequest{Code: &code, Name: optional(*agentName), Harness: harnessOf(session, inSession, *harness)})
	if err != nil {
		return err
	}
	board, agent := joined.Board, joined.Agent
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: board.Name, Name: agent.Name, Token: joined.Token}); err != nil {
		return err
	}
	useAs := useFor(agent.Name)
	if inSession {
		if err := a.bindSession(ctx, session, delivery.AgentRef{Server: srv.URL, Board: board.Name, Name: agent.Name}); err != nil {
			return err
		}
		useAs.BoundSession = optional(session.String())
	}
	previous, err := a.linkProject(projectFile{Server: srv, Board: board.Name})
	if err != nil {
		return err
	}
	roleCharter := ""
	if role, ok := board.Roles[deref(agent.Role)]; ok {
		roleCharter = deref(role.Charter)
	}
	how := fmt.Sprintf("Act as this agent with --as %s, or set ABOARD_AGENT=%s.\n", agent.Name, agent.Name)
	if inSession {
		how = fmt.Sprintf("This session acts as %s, and messages for %s arrive here.\n", agent.Name, agent.Name)
	}
	text := fmt.Sprintf("Joined board %s as %s (owner %s)\n", board.Name, agent.Name, deref(agent.Owner)) + how +
		relinkedText(board.Name, previous)
	a.emit(struct {
		Server        serverRef     `json:"server"`
		Board         api.Board     `json:"board"`
		Agent         api.Member    `json:"agent"`
		Use           agentUse      `json:"use"`
		Charter       string        `json:"charter"`
		RoleCharter   string        `json:"role_charter"`
		PolicyNotice  *policyNotice `json:"policy_notice"`
		PreviousBoard *string       `json:"previous_board"`
	}{srv, board, agent, useAs, board.Charter, roleCharter, noticeFor(board.Policy), optional(previous)}, text)
	return nil
}

// joinTarget finds the server and join code in a join line, or takes a bare code for
// the project's server.
func (a *app) joinTarget(text string) (serverRef, string, error) {
	line, lineErr := joinline.Parse(text)
	if lineErr == nil {
		srv, err := mapJoinServer(line.Server, a.localAddr())
		return srv, line.Code, err
	}
	code, ok := ids.NormalizeJoinCode(text)
	if !ok {
		return serverRef{}, "", &Error{
			Code:    "join_line_invalid",
			Message: "That isn't a join line or a join code.",
			Hint:    `Paste the whole line, such as "Join Aboard board docs on localhost as reviewer with code 7Q4-K2M", or just the code.`,
			Err:     lineErr,
		}
	}
	srv := a.localServer()
	p, found, err := a.readProject()
	if err != nil {
		return serverRef{}, "", err
	}
	if found && p.Server.URL != "" {
		srv = p.Server
	}
	return srv, code, nil
}

// refuseIfLinked stops pair from quietly replacing the board this directory is linked
// to. With --new it lets pair create another board.
func (a *app) refuseIfLinked(newBoard bool) error {
	p, found, err := a.readProject()
	if err != nil || !found || newBoard || p.Board == "" {
		return err
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	agents := creds.names(p.Server.URL, p.Board)
	hint := "To create another board and link this directory to it instead, run aboard pair --new."
	if len(agents) > 0 {
		hint = "To keep working there, act as one of your agents on it with --as (" + strings.Join(agents, ", ") +
			"). " + hint
	}
	e := newError("board_already_linked",
		"This directory is already linked to board "+p.Board+" on "+p.Server.URL+".", hint)
	e.Details = map[string]any{"board": p.Board, "server": p.Server.URL, "agents": agents}
	return e
}
