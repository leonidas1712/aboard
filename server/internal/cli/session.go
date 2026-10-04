package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Where a command's board came from when the agent decided it.
const boardFromAgent = "agent"

// Where the acting agent came from when the session's binding decided it.
const agentFromSession = "session"

// sessionKey returns the harness session this command runs in: ABOARD_SESSION, which
// Aboard's hooks write, then the variable of a harness that gives every command its
// session id. In a sub-agent that has a thread of its own (Codex's), it is the root
// conversation's session, as for a subagent of any other harness.
func (a *app) sessionKey() (delivery.SessionKey, bool) {
	return a.registry().RootSession(a.henv())
}

// serverRefFor names a server URL the way output does.
func (a *app) serverRefFor(url string) serverRef {
	if url == a.localServer().URL {
		return a.localServer()
	}
	return serverRef{Name: url, URL: url}
}

// agentTarget resolves the agent an agent command acts as, and so its board: --as, then
// ABOARD_AGENT, then the agent bound to the current session. The agent decides the
// board, since each agent belongs to one board.
func (a *app) agentTarget(ctx context.Context, boardFlag, asFlag string) (target, agentCredential, error) {
	creds, err := a.readCredentials()
	if err != nil {
		return target{}, agentCredential{}, err
	}
	name := asFlag
	if name == "" {
		name = a.env.Getenv("ABOARD_AGENT")
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name != "" {
		return a.agentByName(creds, name, boardFlag)
	}
	if key, ok := a.sessionKey(); ok {
		t, cred, found, err := a.sessionAgent(ctx, creds, key, boardFlag)
		if err != nil || found {
			return t, cred, err
		}
	}
	t, err := a.selectBoard(boardFlag)
	if err != nil {
		return target{}, agentCredential{}, err
	}
	cred, err := resolveAgent("", "", creds, t)
	return t, cred, err
}

// agentByName finds this machine's agent with a name. A name on several boards is
// narrowed by --board, then by the board of this directory's .aboard file.
func (a *app) agentByName(creds credentials, name, boardFlag string) (target, agentCredential, error) {
	var cands []agentCredential
	for _, c := range creds.Agents {
		if c.Name == name && (boardFlag == "" || c.Board == boardFlag) {
			cands = append(cands, c)
		}
	}
	if len(cands) > 1 {
		if p, ok, err := a.readProject(); err == nil && ok {
			var here []agentCredential
			for _, c := range cands {
				if c.Board == p.Board && (p.Server.URL == "" || c.Server == p.Server.URL) {
					here = append(here, c)
				}
			}
			if len(here) == 1 {
				return target{server: a.serverRefFor(here[0].Server), board: here[0].Board, source: boardFromProject}, here[0], nil
			}
		}
	}
	switch len(cands) {
	case 1:
		source := boardFromAgent
		if boardFlag != "" {
			source = boardFromFlag
		}
		return target{server: a.serverRefFor(cands[0].Server), board: cands[0].Board, source: source}, cands[0], nil
	case 0:
		t, err := a.selectBoard(boardFlag)
		if err != nil {
			e := newError("agent_not_selected", "This machine has no agent named "+name+".",
				"Join a board with aboard join and a join line, or pass --as with one of your agents.")
			e.Details = map[string]any{"choices": []string{}, "board_source": selectedNone}
			return target{}, agentCredential{}, e
		}
		cred, err := resolveAgent(name, "", creds, t)
		return t, cred, err
	}
	boards := make([]string, 0, len(cands))
	for _, c := range cands {
		boards = append(boards, c.Board)
	}
	slices.Sort(boards)
	e := newError("agent_ambiguous",
		fmt.Sprintf("This machine has an agent named %s on %d boards: %s.", name, len(boards), strings.Join(boards, ", ")),
		"Pass --board with one of them.")
	e.Details = map[string]any{"boards": boards}
	return target{}, agentCredential{}, e
}

// sessionAgent returns the agent bound to the session. found is false when the session
// holds no agent (on the --board board, if given). A session holds at most one agent.
func (a *app) sessionAgent(ctx context.Context, creds credentials, key delivery.SessionKey, boardFlag string) (t target, cred agentCredential, found bool, err error) {
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return target{}, agentCredential{}, false, err
	}
	var matching []delivery.AgentRef
	for _, ag := range agents {
		if boardFlag == "" || ag.Board == boardFlag {
			matching = append(matching, ag)
		}
	}
	if len(matching) == 0 {
		return target{}, agentCredential{}, false, nil
	}
	ag := matching[0]
	c, ok := creds.find(ag.Server, ag.Board, ag.Name)
	if !ok {
		return target{}, agentCredential{}, false, newError("agent_not_selected",
			"This session is bound to "+ag.Name+" on board "+ag.Board+", but this machine has no credentials for it.",
			"Join the board again with aboard join, or pass --as with one of your agents.")
	}
	source := boardFromAgent
	if boardFlag != "" {
		source = boardFromFlag
	}
	return target{server: a.serverRefFor(ag.Server), board: ag.Board, source: source}, c, true, nil
}

// sessionAgents asks the daemon which agents are bound to the session.
func (a *app) sessionAgents(ctx context.Context, key delivery.SessionKey) ([]delivery.AgentRef, error) {
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID})
	if err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// checkSession makes sure the session this command runs in can have an agent bound to
// it, before an agent is created for it. A session whose id every command carries is
// registered from here, which checks it in the harness (a Codex thread is read through
// Codex and refused if it is a sub-agent).
func (a *app) checkSession(ctx context.Context) (delivery.SessionKey, bool, error) {
	key, ok := a.sessionKey()
	if !ok {
		return key, false, nil
	}
	op := delivery.OpAgents
	if h, known := a.registry().Get(key.Harness); known && h.Profile().Identity.Kind == "env" {
		op = delivery.OpRegister
	}
	_, err := a.callDaemon(ctx, delivery.Request{Op: op, Harness: key.Harness, Session: key.ID})
	return key, true, err
}

// sessionSources says how each harness gives its commands their session, such as "the
// Claude Code hooks from aboard init set ABOARD_SESSION; Codex sets CODEX_THREAD_ID".
func (a *app) sessionSources() string {
	var parts []string
	for _, h := range a.registry() {
		p := h.Profile()
		switch p.Identity.Kind {
		case "env":
			parts = append(parts, p.Name+" sets "+p.Identity.Env)
		case "hook":
			parts = append(parts, "the "+p.Name+" hooks from aboard init set ABOARD_SESSION")
		case "extension":
			parts = append(parts, "Aboard's "+p.Name+" extension sets ABOARD_SESSION")
		}
	}
	return strings.Join(parts, "; ")
}

// previousAgent is the agent a session was bound to before a command moved it to
// another one.
type previousAgent struct {
	Name  string `json:"name"`
	Board string `json:"board"`
}

// bindSession binds an agent to the session, so its messages are delivered there. A
// session holds one agent at a time; the one it held before, if any, is returned.
func (a *app) bindSession(ctx context.Context, key delivery.SessionKey, agent delivery.AgentRef) (*previousAgent, error) {
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpBind, Harness: key.Harness, Session: key.ID, Agent: &agent})
	if err != nil {
		e := asError(err)
		e.Hint = strings.TrimSpace(e.Hint + " The agent " + agent.Name + " exists; bind it to a session later with aboard resume " + agent.Name + ".")
		return nil, e
	}
	if resp.Previous == nil {
		return nil, nil
	}
	return &previousAgent{Name: resp.Previous.Name, Board: resp.Previous.Board}, nil
}

// movedText is the line saying a session moved from one agent to another, or "".
func movedText(prev *previousAgent, name, board string) string {
	if prev == nil {
		return ""
	}
	return fmt.Sprintf("This session was %s on %s; it is now %s on %s.\n", prev.Name, prev.Board, name, board)
}

// runResume binds the current session to an existing agent on this machine, so its
// unread messages, and any bundle left unconfirmed, are delivered here.
func runResume(ctx context.Context, a *app, args []string) error {
	use := usageOf("resume")
	fs := a.flags("resume")
	boardFlag := fs.String("board", "", "the board, when the agent's name is used on several")
	pos, err := a.parse(fs, args, use, 1, 1)
	if err != nil {
		return err
	}
	key, ok := a.sessionKey()
	if !ok {
		return newError("session_unknown",
			"aboard resume binds an agent to the session it runs in, and this command isn't running in a "+harness.OrList(a.registry().Titles())+" session.",
			"Run it from inside the session ("+a.sessionSources()+"), or use --as AGENT on each command instead.")
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	t, cred, err := a.agentByName(creds, strings.TrimPrefix(pos[0], "@"), *boardFlag)
	if err != nil {
		return err
	}
	ref := delivery.AgentRef{Server: t.server.URL, Board: cred.Board, Name: cred.Name}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpBind, Harness: key.Harness, Session: key.ID, Agent: &ref})
	if err != nil {
		return err
	}
	var prev *previousAgent
	if resp.Previous != nil {
		prev = &previousAgent{Name: resp.Previous.Name, Board: resp.Previous.Board}
	}
	a.emit(struct {
		Agent         string         `json:"agent"`
		Board         string         `json:"board"`
		Session       string         `json:"session"`
		PreviousAgent *previousAgent `json:"previous_agent"`
	}{cred.Name, cred.Board, key.String(), prev},
		fmt.Sprintf("Resumed %s on %s in this session.\n", cred.Name, cred.Board)+movedText(prev, cred.Name, cred.Board))
	return nil
}
