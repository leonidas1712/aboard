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
	name := serverHostName(url)
	if url == a.localServer().URL {
		name = localServerName
	}
	if known, _, err := a.knownServers(); err == nil {
		for _, k := range known {
			if k.URL == url {
				return k.ref()
			}
		}
	}
	return serverRef{Name: name, URL: url}
}

// agentTarget resolves the agent an agent command acts as, and so its board: --as, then
// ABOARD_AGENT, then the agent bound to the current session. The agent decides the
// board, since each agent belongs to one board.
func (a *app) agentTarget(ctx context.Context, boardFlag, asFlag string) (target, agentCredential, error) {
	if _, err := a.agentIssuer(); err != nil {
		return target{}, agentCredential{}, err
	}
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
		if err := a.oneSeat(ctx, boardFlag, name); err != nil {
			return target{}, agentCredential{}, err
		}
		return a.agentByName(creds, name, boardFlag)
	}
	if key, ok := a.sessionKey(); ok {
		t, cred, found, err := a.sessionAgent(ctx, creds, key, boardFlag)
		if err != nil || found {
			return t, cred, err
		}
	}
	return target{}, agentCredential{}, newError("agent_not_selected", "No working agent seat matches this selection.", "Run aboard status to see bound seats, or name a saved seat with --as, --server and --board.")
}

// oneSeat checks remaining bound seats after explicit name, issuer and board filters.
func (a *app) oneSeat(ctx context.Context, boardFlag, name string) error {
	key, ok := a.sessionKey()
	if !ok {
		return nil
	}
	seats, err := a.sessionAgents(ctx, key)
	if err != nil {
		return err
	}
	matching, err := a.filterAgentSeats(seats, boardFlag)
	if err != nil {
		return err
	}
	named := matching[:0]
	for _, seat := range matching {
		if seat.Name == name {
			named = append(named, seat)
		}
	}
	return qualifiedSeatAmbiguity(named)
}

// agentByName finds this machine's agent with a name. A name on several boards is
// narrowed by explicit issuer and board, never by the working directory.
func (a *app) agentByName(creds credentials, name, boardFlag string) (target, agentCredential, error) {
	issuer, err := a.agentIssuer()
	if err != nil {
		return target{}, agentCredential{}, err
	}
	var cands []agentCredential
	issuers := map[string]bool{}
	for _, c := range creds.Agents {
		issuers[c.Server] = true
	}
	a.qualifyAgentOutput = len(issuers) > 1
	for _, c := range creds.Agents {
		if c.Name == name && (boardFlag == "" || c.Board == boardFlag) && (issuer == "" || c.Server == issuer) {
			cands = append(cands, c)
		}
	}
	switch len(cands) {
	case 1:
		source := boardFromAgent
		if boardFlag != "" {
			source = boardFromFlag
		}
		server := a.serverRefFor(cands[0].Server)
		a.selectedAgentServer = &server
		return target{server: server, board: cands[0].Board, source: source}, cands[0], nil
	case 0:
		e := newError("agent_not_selected", "This machine has no matching seat for "+name+".", "Choose a saved agent with --as, --server and --board; this command never uses a person's login.")
		e.Details = map[string]any{"choices": []string{}, "board_source": selectedNone}
		return target{}, agentCredential{}, e
	}
	boards := make([]string, 0, len(cands))
	choices := make([]map[string]string, 0, len(cands))
	for _, c := range cands {
		boards = append(boards, c.Board)
		choices = append(choices, map[string]string{"server": c.Server, "board": c.Board, "name": c.Name, "command": "aboard status --as " + commandWord(c.Name) + " --server " + commandWord(c.Server) + " --board " + commandWord(c.Board)})
	}
	slices.Sort(boards)
	e := newError("agent_ambiguous", fmt.Sprintf("This machine has %d matching seats for %s.", len(cands), name), "Choose one with --server and --board.")
	e.Details = map[string]any{"boards": boards, "seats": choices}
	return target{}, agentCredential{}, e
}

// sessionAgent returns the agent bound to the session. found is false when the session
// holds no matching agent after issuer and board filters.
func (a *app) sessionAgent(ctx context.Context, creds credentials, key delivery.SessionKey, boardFlag string) (t target, cred agentCredential, found bool, err error) {
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return target{}, agentCredential{}, false, err
	}
	boundCount := len(agents)
	agents, err = a.filterAgentSeats(agents, boardFlag)
	if err != nil {
		return target{}, agentCredential{}, false, err
	}
	if err := qualifiedSeatAmbiguity(agents); err != nil {
		return target{}, agentCredential{}, false, err
	}
	var matching []delivery.AgentRef
	for _, ag := range agents {
		if boardFlag == "" || ag.Board == boardFlag {
			matching = append(matching, ag)
		}
	}
	if len(matching) == 0 {
		if boundCount > 0 && (boardFlag != "" || a.agentServerFlag != "") {
			return target{}, agentCredential{}, false, newError("agent_not_selected", "No bound agent seat matches this issuer and board.", "Run aboard status to see current seats, then pass their --server and --board.")
		}
		return target{}, agentCredential{}, false, nil
	}
	ag := matching[0]
	c, ok := creds.forSeat(ag)
	if !ok {
		return target{}, agentCredential{}, false, newError("agent_not_selected",
			"This session is bound to "+ag.Name+" on board "+ag.Board+", but this machine has no credentials for it.",
			"Join the board again with aboard join, or pass --as with one of your agents.")
	}
	source := boardFromAgent
	if boardFlag != "" {
		source = boardFromFlag
	}
	server := a.serverRefFor(ag.Server)
	a.selectedAgentServer = &server
	return target{server: a.serverRefFor(ag.Server), board: ag.Board, source: source}, c, true, nil
}

func (a *app) agentIssuer() (string, error) {
	if a.agentServerFlag == "" {
		return "", nil
	}
	issuer, err := a.namedServer(a.agentServerFlag)
	if err != nil {
		return "", err
	}
	return issuer.URL, nil
}

func (a *app) filterAgentSeats(seats []delivery.AgentRef, board string) ([]delivery.AgentRef, error) {
	issuer, err := a.agentIssuer()
	if err != nil {
		return nil, err
	}
	issuers := map[string]bool{}
	for _, seat := range seats {
		issuers[seat.Server] = true
	}
	a.qualifyAgentOutput = len(issuers) > 1
	filtered := []delivery.AgentRef{}
	for _, seat := range seats {
		if (issuer == "" || seat.Server == issuer) && (board == "" || seat.Board == board) {
			filtered = append(filtered, seat)
		}
	}
	return filtered, nil
}

func qualifiedSeatAmbiguity(seats []delivery.AgentRef) error {
	if len(seats) < 2 {
		return nil
	}
	sorted := slices.Clone(seats)
	slices.SortFunc(sorted, func(a, b delivery.AgentRef) int {
		return strings.Compare(a.Server+"\x00"+a.Board, b.Server+"\x00"+b.Board)
	})
	boards := []string{}
	choices := []map[string]string{}
	for _, seat := range sorted {
		boards = append(boards, seat.Board)
		choices = append(choices, map[string]string{"server": seat.Server, "board": seat.Board, "name": seat.Name, "command": "aboard status --server " + commandWord(seat.Server) + " --board " + commandWord(seat.Board)})
	}
	e := newError("board_ambiguous", "This selection has several bound agent seats.", "Choose one with --server and --board, or run aboard status to see every seat.")
	e.Details = map[string]any{"boards": boards, "seats": choices}
	return e
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
		e.Hint = strings.TrimSpace(e.Hint + " The agent " + agent.Name + " exists; bind it to a session later with aboard resume " + commandWord(agent.Name) + " --server " + commandWord(agent.Server) + " --board " + commandWord(agent.Board) + ".")
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
	serverFlag := fs.String("server", "", "the issuer of the saved agent seat")
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
	if *serverFlag != "" {
		srv, err := a.namedServer(*serverFlag)
		if err != nil {
			return err
		}
		creds.Agents = slices.DeleteFunc(creds.Agents, func(c agentCredential) bool { return c.Server != srv.URL })
	}
	t, cred, err := a.agentByName(creds, strings.TrimPrefix(pos[0], "@"), *boardFlag)
	if err != nil {
		return err
	}
	ref := delivery.AgentRef{Server: t.server.URL, Board: cred.Board, Name: cred.Name, MemberID: cred.MemberID}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpBind, Harness: key.Harness, Session: key.ID, Agent: &ref})
	if err != nil {
		return err
	}
	var prev *previousAgent
	if resp.Previous != nil {
		prev = &previousAgent{Name: resp.Previous.Name, Board: resp.Previous.Board}
	}
	held := ""
	if h, ok := a.heldMode(ctx, t, cred); ok {
		held = string(h.Mode)
	}
	mode := a.deliveryFor(ctx, ref, held)
	a.emit(struct {
		Agent         string         `json:"agent"`
		Board         string         `json:"board"`
		Session       string         `json:"session"`
		PreviousAgent *previousAgent `json:"previous_agent"`
		seatDelivery
	}{cred.Name, cred.Board, key.String(), prev, mode},
		fmt.Sprintf("Resumed %s on %s in this session.\n", cred.Name, cred.Board)+movedText(prev, cred.Name, cred.Board)+mode.line()+a.seatBoardReminder(ctx, key, cred.Board))
	return nil
}
