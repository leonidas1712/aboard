package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
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
		Server      serverRef   `json:"server"`
		Board       *string     `json:"board"`
		BoardSource string      `json:"board_source"`
		Agent       *string     `json:"agent"`
		AgentSource string      `json:"agent_source"`
		Agents      []string    `json:"agents"`
		Policy      *api.Policy `json:"policy"`
	}{Server: a.localServer(), BoardSource: selectedNone, AgentSource: selectedNone, Agents: []string{}}

	t, err := a.selectBoard(*boardFlag)
	if err != nil {
		if asError(err).Code != "board_not_selected" {
			return err
		}
		a.emit(out, "Board:  none; run aboard pair or aboard join here, or pass --board\n")
		return nil
	}
	out.Server, out.Board, out.BoardSource = t.server, &t.board, t.source

	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	out.Agents = creds.names(t.server.URL, t.board)
	name, source := *as, agentFromFlag
	if name == "" {
		name, source = a.env.Getenv("ABOARD_AGENT"), agentFromEnv
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")

	var text strings.Builder
	fmt.Fprintf(&text, "Board:  %s on %s (%s)\n", t.board, t.server.URL, sourceText(t.source))
	switch _, known := creds.find(t.server.URL, t.board, name); {
	case name == "":
		fmt.Fprintf(&text, "Agent:  none selected; pass --as or set ABOARD_AGENT (yours here: %s)\n", namesText(out.Agents))
	case !known:
		fmt.Fprintf(&text, "Agent:  %s is not one of your agents here (yours here: %s)\n", name, namesText(out.Agents))
	default:
		out.Agent, out.AgentSource = &name, source
		label := "--as"
		if source == agentFromEnv {
			label = "ABOARD_AGENT"
		}
		fmt.Fprintf(&text, "Agent:  %s (from %s)\n", name, label)
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
