package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
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
	const use = "aboard status [--as AGENT] [--board NAME] [--json]"
	fs := a.flags("status")
	as := fs.String("as", "", "the agent to check")
	boardFlag := fs.String("board", "", "the board to check")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	out := struct {
		Server        serverRef    `json:"server"`
		ServerRunning bool         `json:"server_running"`
		Daemon        daemonReport `json:"daemon"`
		Board         *string      `json:"board"`
		BoardSource   string       `json:"board_source"`
		Agent         *string      `json:"agent"`
		AgentSource   string       `json:"agent_source"`
		Delivery      *string      `json:"delivery"`
		Agents        []string     `json:"agents"`
		Policy        *api.Policy  `json:"policy"`
	}{Server: a.localServer(), BoardSource: selectedNone, AgentSource: selectedNone, Agents: []string{}}

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

	t, err := a.selectBoard(*boardFlag)
	switch {
	case agentBoard != nil && (err != nil || t.board != agentBoard.board || t.server.URL != agentBoard.server.URL):
		t = *agentBoard
	case err != nil:
		if asError(err).Code != "board_not_selected" {
			return err
		}
		var text strings.Builder
		a.runningLines(ctx, &text, &out.ServerRunning, &out.Daemon, out.Server)
		text.WriteString("Board:  none; run aboard pair or aboard join here, or pass --board\n")
		a.emit(out, text.String())
		return nil
	}
	out.Server, out.Board, out.BoardSource = t.server, &t.board, t.source
	out.Agents = creds.names(t.server.URL, t.board)

	var text strings.Builder
	a.runningLines(ctx, &text, &out.ServerRunning, &out.Daemon, t.server)
	fmt.Fprintf(&text, "Board:  %s on %s (%s)\n", t.board, t.server.URL, sourceText(t.source))
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
		fmt.Fprintf(&text, "Agent:  %s (from %s); delivery %s\n", name, label, m)
	}

	if c, err := a.humanClient(t); err == nil {
		ctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		if b, err := c.board(ctx, t.board); err == nil {
			out.Policy = &b.Policy
			line := string(b.Policy.Preset)
			if b.Policy.Preset == "starter" {
				line += " (a starting point; tighten with aboard board policy recommended)"
			}
			fmt.Fprintf(&text, "Policy: %s\n", line)
		}
	}
	a.emit(out, text.String())
	return nil
}

func namesText(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// daemonReport is the delivery daemon's part of aboard status.
type daemonReport struct {
	Running      bool `json:"running"`
	PID          *int `json:"pid"`
	OpenSessions int  `json:"open_sessions"`
}

// runningLines writes the Server and Daemon lines of aboard status. It checks both
// without starting either.
func (a *app) runningLines(ctx context.Context, text *strings.Builder, running *bool, d *daemonReport, srv serverRef) {
	*running = a.serverAnswers(ctx, srv)
	local := srv.URL == a.localServer().URL
	switch {
	case *running && local:
		fmt.Fprintf(text, "Server: %s running\n", srv.URL)
	case *running:
		fmt.Fprintf(text, "Server: %s reachable\n", srv.URL)
	case local:
		fmt.Fprintf(text, "Server: %s not running; aboard up starts it\n", srv.URL)
	default:
		fmt.Fprintf(text, "Server: %s unreachable\n", srv.URL)
	}
	st, _ := a.daemonStatus(ctx)
	if st == nil {
		text.WriteString("Daemon: not running; it starts when a session or command needs it\n")
		return
	}
	pid := st.PID
	*d = daemonReport{Running: true, PID: &pid, OpenSessions: st.OpenSessions}
	fmt.Fprintf(text, "Daemon: running (pid %d), %d open %s\n", pid, st.OpenSessions, plural(st.OpenSessions, "session", "sessions"))
}
