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
		// DeliveryApplied is the mode the agent's delivery daemon last reported applying,
		// when the server has one.
		DeliveryApplied *string `json:"delivery_applied"`
		// DeliveryUnconfirmed is true when the server couldn't be read, so the mode is the
		// one this machine kept.
		DeliveryUnconfirmed bool        `json:"delivery_unconfirmed"`
		Presence            *string     `json:"presence"`
		Agents              []string    `json:"agents"`
		Policy              *api.Policy `json:"policy"`
		People              []person    `json:"people"`
		Subagent            *subagentOf `json:"subagent"`
		// Seats and SeatsUsage are set only in a session with several seats, with
		// neither --board nor --as; one seat or none leaves them out.
		Work       *api.AgentWork       `json:"work,omitempty"`
		Nudges     []deliverytext.Nudge `json:"nudges,omitempty"`
		Seats      []seatRow            `json:"seats,omitempty"`
		SeatsUsage []string             `json:"seats_usage,omitempty"`
		// Added are the boards the person was added to that are still new to them.
		Added  []addedNotice            `json:"added,omitempty"`
		Queued *delivery.QueuedMessages `json:"queued,omitempty"`
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
	var selectedCred *agentCredential
	switch {
	case name != "":
		if err := a.oneSeat(ctx, *boardFlag); err != nil {
			return err
		}
		t, cred, err := a.agentByName(creds, name, *boardFlag)
		if err != nil {
			return err
		}
		agentBoard, selectedCred = &t, &cred
	default:
		if key, ok := a.sessionKey(); ok {
			if agents, err := a.sessionAgents(ctx, key); err == nil && len(agents) > 1 && *boardFlag == "" {
				// A session with several seats selects none: status lists them all.
				out.AgentSource = agentFromSession
				out.Seats = a.sessionSeats(ctx, agents, creds)
				var text strings.Builder
				a.runningLines(ctx, &text, &out.ServerRunning, &out.SandboxBlocks, &out.Daemon, a.serverRefFor(agents[0].Server))
				out.Server = a.serverRefFor(agents[0].Server)
				text.WriteString(setupLine)
				seats, usage := seatsText(agents[0].Server, out.Seats)
				out.SeatsUsage = usage
				text.WriteString(seats)
				out.Queued = a.statusQueued(ctx, "", "")
				text.WriteString(queuedText(out.Queued))
				a.emit(out, styleStatus(text.String(), a.out()))
				return nil
			}
			t, cred, found, err := a.sessionAgent(ctx, creds, key, *boardFlag)
			if err != nil && asError(err).Code != "sandbox_blocks_network" {
				return err
			}
			if found {
				agentBoard, name, source = &t, cred.Name, agentFromSession
				selectedCred = &cred
			}
		}
	}

	var subLine string
	if _, sub := a.registry().Subagent(a.henv()); sub {
		out.Subagent, subLine = a.subagentStatus(ctx, creds)
	}

	t, err := a.selectBoard(*boardFlag)
	if agentBoard == nil && !a.agentSelected(*as) {
		srv, resolveErr := a.resolveServer("")
		if resolveErr != nil {
			return resolveErr
		}
		out.Server = srv
		t, err = a.humanBoard(ctx, *boardFlag)
	}
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
		out.Added = a.addedAround(ctx, out.Server, *as)
		text.WriteString(addedText(out.Added))
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
	var c *client
	if selectedCred != nil {
		c, _ = a.client(ctx, t.server, selectedCred.Token, requestTimeout)
	} else if !a.agentSelected(*as) {
		c, _ = a.humanClient(ctx, t)
	}
	if c != nil {
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
		if r, err := c.api.ListMembersWithResponse(ctx, t.board, nil); err == nil && r.JSON200 != nil {
			members = r.JSON200.Members
			if out.People = peopleOf(members); out.People != nil {
				fmt.Fprintf(&boardLines, "People: %s\n", peopleText(out.People))
			}
		}
	}

	cred, known := creds.find(t.server.URL, t.board, name)
	if selectedCred != nil && selectedCred.Server == t.server.URL && selectedCred.Board == t.board {
		cred, known = *selectedCred, true
	}
	switch {
	case name == "":
		fmt.Fprintf(&text, "Agent:  none selected; pass --as or set ABOARD_AGENT (yours here: %s)\n", namesText(out.Agents))
	case !known:
		fmt.Fprintf(&text, "Agent:  %s is not one of your agents here (yours here: %s)\n", name, namesText(out.Agents))
	default:
		out.Agent, out.AgentSource = &name, source
		label := map[string]string{agentFromFlag: "--as", agentFromEnv: "ABOARD_AGENT", agentFromSession: "this session"}[source]
		// The mode the agent's server holds, or, from a server that doesn't hold modes,
		// this machine's.
		m := ""
		var applied *string
		for _, mem := range members {
			if cred.MemberID != "" && mem.Id == cred.MemberID && mem.Kind == api.MemberKindAgent {
				m = heldModeOf(mem)
				out.Presence = presenceOf([]api.Member{mem}, mem.Name)
				if mem.Delivery != nil {
					if parsed, ok := delivery.ParseMode(string(*mem.Delivery)); ok {
						s := string(parsed)
						applied = &s
					}
				}
			}
		}
		if m == "" {
			mode, err := a.deliveryMode(ctx, delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: name, MemberID: cred.MemberID})
			if err != nil {
				return err
			}
			m = string(mode)
			// No members read means the server couldn't be: the mode is this machine's.
			out.DeliveryUnconfirmed = members == nil
		}
		out.Delivery, out.DeliveryApplied = &m, applied
		line := fmt.Sprintf("Agent:  %s (from %s); delivery %s", name, label, m)
		if out.DeliveryUnconfirmed {
			line += keptHereText
		}
		if applied != nil && *applied != m && out.Presence != nil && *out.Presence != "no_session" {
			// A daemon from an older aboard keeps its own mode; say what it does.
			line += fmt.Sprintf(" (its delivery daemon applies %s)", *applied)
		}
		if out.Presence != nil {
			line += "; " + presenceText(*out.Presence)
		}
		rule := deliverytext.ModeRule(m)
		out.DeliveryRule = &rule
		text.WriteString(line + "\n        " + rule + "\n")
	}
	text.WriteString(subLine)
	text.WriteString(boardLines.String())
	if known && out.Agent != nil {
		if c, err := a.client(ctx, t.server, cred.Token, requestTimeout); err == nil {
			in := c.taskInbox(ctx)
			if in != nil {
				out.Work = in.Work
			}
			out.Nudges = a.taskNudges(ctx, c, delivery.AgentRef{Server: t.server.URL, Board: t.board, MemberID: cred.MemberID}, in, "status", *boardFlag != "")
			text.WriteString(nudgesText(out.Nudges))
		}
	}
	out.Added = a.addedAround(ctx, out.Server, *as)
	text.WriteString(addedText(out.Added))
	if selectedCred != nil {
		out.Queued = a.statusQueued(ctx, selectedCred.Server, selectedCred.MemberID)
		text.WriteString(queuedText(out.Queued))
	}
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
	DisplayName *string `json:"display_name,omitempty"`
	Name        string  `json:"name"`
	Access      string  `json:"access"`
}

// peopleOf returns the people among a board's members, or nil when there is only one:
// someone alone on their board never needs to hear about access levels.
func peopleOf(members []api.Member) []person {
	var people []person
	for _, m := range members {
		if m.Kind == api.MemberKindHuman && m.Access != nil {
			people = append(people, person{Name: m.Name, Access: string(*m.Access), DisplayName: m.DisplayName})
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
		parts[i] = p.Name
		if p.DisplayName != nil && *p.DisplayName != "" {
			parts[i] = "@" + p.Name + " (" + *p.DisplayName + ")"
		}
		parts[i] += " (" + p.Access + ")"
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
	// StoppedAgents are agents whose deliveries the daemon stopped, with why.
	StoppedAgents []stoppedAgent `json:"stopped_agents,omitempty"`
}

// stoppedAgent is an agent whose deliveries the daemon stopped, such as one whose board
// is gone.
type stoppedAgent struct {
	Server string `json:"server"`
	Board  string `json:"board"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
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
	for _, p := range st.Agents {
		d.StoppedAgents = append(d.StoppedAgents, stoppedAgent{Server: p.Agent.Server, Board: p.Agent.Board, Name: p.Agent.Name, Reason: p.Reason})
		if p.Reason == delivery.ReasonExtensionOutdated {
			fmt.Fprintln(text, "        The harness extension cannot deliver to several boards. Run aboard init, then restart the harness.")
			continue
		}
		if p.Reason == delivery.ReasonServerUnreachable {
			fmt.Fprintf(text, "        deliveries for %s on %s wait for %s, which can't be reached; they resume once it answers\n", p.Agent.Name, p.Agent.Board, p.Agent.Server)
			continue
		}
		if p.Reason == delivery.ReasonHandoffFailed {
			fmt.Fprintf(text, "        deliveries for %s on %s can't be prepared for its session; run aboard resume in that session (see aboard doctor)\n", p.Agent.Name, p.Agent.Board)
			continue
		}
		if p.Reason == delivery.ReasonBoardGone {
			fmt.Fprintf(text, "        %s. Join again with a new agent (aboard join) if the person still belongs on it.\n", boardGoneText(p.Agent.Name, p.Agent.Board))
			continue
		}
		fmt.Fprintf(text, "        deliveries for %s on %s stopped (%s); see aboard doctor\n", p.Agent.Name, p.Agent.Board, p.Reason)
	}
}
