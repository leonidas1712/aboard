package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Where the acting agent came from.
const (
	agentFromFlag = "flag"
	agentFromEnv  = "env"
	selectedNone  = "none"
)

// runStatus prints which board and agent commands run here would use, and where each
// came from. It is the one place, besides selection errors, that shows the sources.
func runStatus(ctx context.Context, a *app, args []string) error {
	use := usageOf("status")
	fs := a.flags("status")
	as := fs.String("as", "", "the agent to check")
	boardFlag := fs.String("board", "", "the board to check")
	launch := fs.String("launch", "", "the launch ticket from the session's first prompt")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if *launch != "" {
		if !launchtickets.Valid(*launch) {
			return usageError(fmt.Sprintf("%q is not a launch ticket: one is lch_ and 24 hex digits.", *launch), use)
		}
		// The session's first prompt (Codex's, from aboard swarm up) gave the ticket:
		// hand it in so this session takes its seat, unless its prompt hook already has.
		a.claimTicket(ctx, *launch)
	}
	// Like any command that uses them, status first replaces a local server or daemon
	// from an older build, so what it reports is what the next command will use.
	if err := a.replaceOutdatedLocal(ctx); err != nil {
		return err
	}
	if p, err := a.paths(); err == nil {
		a.replaceOutdatedDaemon(ctx, p)
	}
	out := struct {
		Server         serverRef    `json:"server"`
		ServerRunning  bool         `json:"server_running"`
		ServerReplaced *replacement `json:"server_replaced"`
		SandboxBlocks  bool         `json:"sandbox_blocks_network"`
		Daemon         daemonReport `json:"daemon"`
		Setup          setupReport  `json:"setup"`
		Board          *string      `json:"board"`
		BoardSource    string       `json:"board_source"`
		Agent          *string      `json:"agent"`
		AgentSource    string       `json:"agent_source"`
		Delivery       *string      `json:"delivery"`
		DeliveryRule   *string      `json:"delivery_rule"`
		Presence       *string      `json:"presence"`
		Agents         []string     `json:"agents"`
		Policy         *api.Policy  `json:"policy"`
		People         []person     `json:"people"`
		Subagent       *subagentOf  `json:"subagent"`
	}{Server: a.localServer(), ServerReplaced: a.localReplaced, BoardSource: selectedNone, AgentSource: selectedNone, Agents: []string{}}
	var setupLine string
	out.Setup, setupLine = a.setupStatus()

	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	name, source := *as, agentFromFlag
	if name == "" {
		name, source = a.env.Getenv("ABOARD_AGENT"), agentFromEnv
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")

	// The agent decides the board: when one is selected, its board is the one agent
	// commands use, even if this directory names another.
	var agentBoard *target
	switch {
	case name != "":
		if t, _, err := a.agentByName(creds, name, *boardFlag); err == nil {
			agentBoard = &t
		}
	default:
		if key, ok := a.sessionKey(); ok {
			if t, cred, found, err := a.sessionAgent(ctx, creds, key, *boardFlag); err == nil && found {
				agentBoard, name, source = &t, cred.Name, agentFromSession
			}
		}
	}

	var subLine string
	if _, sub := a.registry().Subagent(a.henv()); sub {
		out.Subagent, subLine = a.subagentStatus(ctx, creds)
	}

	t, err := a.selectBoard(*boardFlag)
	switch {
	case agentBoard != nil && (err != nil || t.board != agentBoard.board || t.server.URL != agentBoard.server.URL):
		t = *agentBoard
	case err != nil:
		if asError(err).Code != "board_not_selected" {
			return err
		}
		var text strings.Builder
		a.runningLines(ctx, &text, &out.ServerRunning, &out.SandboxBlocks, &out.Daemon, out.Server)
		text.WriteString(setupLine)
		text.WriteString("Board:  none; run aboard pair or aboard join here, or pass --board\n")
		text.WriteString(subLine)
		a.emit(out, styleStatus(text.String(), a.out()))
		return nil
	}
	out.Server, out.Board, out.BoardSource = t.server, &t.board, t.source
	out.Agents = creds.names(t.server.URL, t.board)

	var text strings.Builder
	a.runningLines(ctx, &text, &out.ServerRunning, &out.SandboxBlocks, &out.Daemon, t.server)
	text.WriteString(setupLine)
	fmt.Fprintf(&text, "Board:  %s on %s (%s)\n", t.board, t.server.URL, sourceText(t.source))
	// The board's lines come from the server, read first so the Agent line can show the
	// agent's presence.
	var boardLines strings.Builder
	var members []api.Member
	if c, err := a.humanClient(ctx, t); err == nil {
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		if b, err := c.board(ctx, t.board); err == nil {
			out.Policy = &b.Policy
			line := string(b.Policy.Preset)
			if b.Policy.Preset == "starter" {
				line += " (a starting point; tighten with aboard board policy recommended)"
			}
			fmt.Fprintf(&boardLines, "Policy: %s\n", line)
		}
		if r, err := c.api.ListMembersWithResponse(ctx, t.board); err == nil && r.JSON200 != nil {
			members = r.JSON200.Members
			if out.People = peopleOf(members); out.People != nil {
				fmt.Fprintf(&boardLines, "People: %s\n", peopleText(out.People))
			}
		}
	}

	switch _, known := creds.find(t.server.URL, t.board, name); {
	case name == "":
		fmt.Fprintf(&text, "Agent:  none selected; pass --as or set ABOARD_AGENT (yours here: %s)\n", namesText(out.Agents))
	case !known:
		fmt.Fprintf(&text, "Agent:  %s is not one of your agents here (yours here: %s)\n", name, namesText(out.Agents))
	default:
		out.Agent, out.AgentSource = &name, source
		label := map[string]string{agentFromFlag: "--as", agentFromEnv: "ABOARD_AGENT", agentFromSession: "this session"}[source]
		mode, err := a.deliveryMode(ctx, delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: name})
		if err != nil {
			return err
		}
		m := string(mode)
		out.Delivery = &m
		line := fmt.Sprintf("Agent:  %s (from %s); delivery %s", name, label, m)
		if out.Presence = presenceOf(members, name); out.Presence != nil {
			line += "; " + presenceText(*out.Presence)
		}
		rule := deliverytext.ModeRule(m)
		out.DeliveryRule = &rule
		text.WriteString(line + "\n        " + rule + "\n")
	}
	text.WriteString(subLine)
	text.WriteString(boardLines.String())
	a.emit(out, styleStatus(text.String(), a.out()))
	return nil
}

// subagentOf is the subagent aboard status runs in: the harness session it belongs to,
// the agent that session holds, which the subagent's commands would act as, and its own
// seat, which it never has today.
type subagentOf struct {
	Harness     string  `json:"harness"`
	Session     string  `json:"session"`
	ParentAgent *string `json:"parent_agent"`
	Seat        *string `json:"seat"`
}

// subagentStatus describes the subagent a command runs in, and its Subagent line. A
// subagent inherits its parent's session, so it finds the parent's agent there.
func (a *app) subagentStatus(ctx context.Context, creds credentials) (so *subagentOf, line string) {
	so = &subagentOf{}
	title := "a harness"
	if key, ok := a.sessionKey(); ok {
		so.Harness, so.Session = key.Harness, key.String()
		if h, known := a.registry().Get(key.Harness); known {
			title = withArticle(h.Profile().Name)
		}
		if _, cred, found, err := a.sessionAgent(ctx, creds, key, ""); err == nil && found {
			so.ParentAgent = &cred.Name
		}
	}
	const reads = "so it may only read: aboard read, aboard status, aboard inbox --peek"
	line = "Subagent: runs in a subagent of " + title + " session, which has no agent; it has no seat of its own, " + reads + "\n"
	if so.ParentAgent != nil {
		line = "Subagent: runs in a subagent of " + title + " session; it would act as " + *so.ParentAgent +
			", the session's agent, but has no seat of its own, " + reads + "\n"
	}
	return so, line
}

// presenceText is a presence in words. An agent with no session is "disconnected": its
// session comes back by itself when the harness resumes it (D157).
func presenceText(p string) string {
	if p == string(api.MemberPresenceNoSession) {
		return "disconnected"
	}
	return strings.ReplaceAll(p, "_", " ")
}

// presenceOf returns the named agent's presence among a board's members, or nil.
func presenceOf(members []api.Member, name string) *string {
	for _, m := range members {
		if m.Name == name && m.Kind == api.MemberKindAgent && m.Presence != nil {
			p := string(*m.Presence)
			return &p
		}
	}
	return nil
}

// person is one person on a board in aboard status, with what they may change there.
type person struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

// peopleOf returns the people among a board's members, or nil when there is only one:
// someone alone on their board never needs to hear about access levels.
func peopleOf(members []api.Member) []person {
	var people []person
	for _, m := range members {
		if m.Kind == api.MemberKindHuman && m.Access != nil {
			people = append(people, person{Name: m.Name, Access: string(*m.Access)})
		}
	}
	if len(people) < 2 {
		return nil
	}
	return people
}

func peopleText(people []person) string {
	parts := make([]string, len(people))
	for i, p := range people {
		parts[i] = p.Name + " (" + p.Access + ")"
	}
	return strings.Join(parts, ", ")
}

func namesText(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// setupReport lists the harnesses whose Aboard hooks are installed, in each scope.
type setupReport struct {
	Global  []string `json:"global"`
	Project []string `json:"project"`
}

// setupStatus reports where aboard init installed the hooks, and the Setup line of
// aboard status. A hook file it can't read counts as not installed; aboard doctor
// reports why.
func (a *app) setupStatus() (r setupReport, line string) {
	r = setupReport{Global: []string{}, Project: []string{}}
	var parts []string
	for _, h := range a.registry() {
		name := h.Profile().Harness
		scopes, _, err := a.installedScopes(h, h.Hooks("aboard", harness.Newest))
		if err != nil || len(scopes) == 0 {
			continue
		}
		var where []string
		for _, s := range scopes {
			if s == scopeGlobal {
				r.Global = append(r.Global, name)
			} else {
				r.Project = append(r.Project, name)
			}
			where = append(where, scopeText(s))
		}
		parts = append(parts, name+" "+strings.Join(where, " and "))
	}
	if len(parts) == 0 {
		return r, "Setup:  none; aboard init adds the skill and hooks\n"
	}
	return r, "Setup:  " + strings.Join(parts, ", ") + "\n"
}

// daemonReport is the delivery daemon's part of aboard status.
type daemonReport struct {
	Running      bool         `json:"running"`
	PID          *int         `json:"pid"`
	OpenSessions int          `json:"open_sessions"`
	Replaced     *replacement `json:"replaced"`
	// Stalled counts deliveries handed to an idle session that started no turn.
	Stalled int `json:"stalled"`
}

// runningLines writes the Server and Daemon lines of aboard status, saying what the
// command replaced. It checks both without starting either. Inside a sandbox that
// blocks the network, one that doesn't answer may still run, so it says that instead
// of "not running" and sets blocked.
func (a *app) runningLines(ctx context.Context, text *strings.Builder, running, blocked *bool, d *daemonReport, srv serverRef) {
	*running = a.serverAnswers(ctx, srv)
	local := srv.URL == a.localServer().URL
	sandbox, noNetwork := a.networkBlocked()
	switch {
	case !*running && noNetwork:
		*blocked = true
		fmt.Fprintf(text, "Server: %s can't be reached from %s's sandbox, which blocks network access; "+
			"run %s in a terminal, or approve this command outside the sandbox\n", srv.URL, sandbox, allowFix)
	case *running && local:
		fmt.Fprintf(text, "Server: %s running%s\n", srv.URL, a.localReplaced.text())
	case *running:
		fmt.Fprintf(text, "Server: %s reachable\n", srv.URL)
	case local:
		fmt.Fprintf(text, "Server: %s not running; aboard up starts it\n", srv.URL)
	default:
		fmt.Fprintf(text, "Server: %s unreachable\n", srv.URL)
	}
	st, _ := a.daemonStatus(ctx)
	if st == nil && noNetwork {
		*blocked = true
		fmt.Fprintf(text, "Daemon: can't be reached from %s's sandbox\n", sandbox)
		return
	}
	if st == nil {
		text.WriteString("Daemon: not running; it starts when a session or command needs it\n")
		return
	}
	pid := st.PID
	*d = daemonReport{Running: true, PID: &pid, OpenSessions: st.OpenSessions, Replaced: a.daemonReplaced, Stalled: len(st.Stalled)}
	stalled := ""
	if n := len(st.Stalled); n > 0 {
		stalled = fmt.Sprintf("; %d %s stalled: handed to an idle session that started no turn (see aboard doctor)", n, plural(n, "delivery", "deliveries"))
	}
	fmt.Fprintf(text, "Daemon: running (pid %d), %d open %s%s%s\n", pid, st.OpenSessions,
		plural(st.OpenSessions, "session", "sessions"), stalled, a.daemonReplaced.text())
}
