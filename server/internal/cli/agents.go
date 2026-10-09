package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

type ownAgentRow struct {
	ID            string             `json:"id"`
	Board         string             `json:"board"`
	Name          string             `json:"name"`
	Location      *api.AgentLocation `json:"location,omitempty"`
	ReopenCommand string             `json:"reopen_command,omitempty"`
	ResumeCommand string             `json:"resume_command"`
}

type ownAgentsGroup struct {
	Server string         `json:"server"`
	Agents []ownAgentRow  `json:"agents"`
	Error  *boardsFailure `json:"error,omitempty"`
}

func runAgents(ctx context.Context, a *app, args []string) error {
	fs := a.flags("agents")
	server := fs.String("server", "", "list your agents on this server")
	as := fs.String("as", "", "use this agent's own-person view")
	board := fs.String("board", "", "the selected agent's board")
	if _, err := a.parse(fs, args, usageOf("agents"), 0, 0); err != nil {
		return err
	}
	groups := []ownAgentsGroup{}
	var text strings.Builder
	if a.agentSelected(*as) {
		var t target
		var cred agentCredential
		var err error
		if key, ok := a.sessionKey(); ok && *as == "" && a.env.Getenv("ABOARD_AGENT") == "" && *board == "" {
			seats, e := a.sessionAgents(ctx, key)
			if e != nil {
				return e
			}
			if len(seats) > 0 {
				creds, e := a.readCredentials()
				if e != nil {
					return e
				}
				var found bool
				cred, found = creds.forSeat(seats[0])
				if !found {
					return usageError("This session has no saved agent credential.", "Run aboard status to check its seats.")
				}
				t.server = a.serverRefFor(cred.Server)
			} else {
				t, cred, err = a.agentTarget(ctx, *board, *as)
			}
		} else {
			t, cred, err = a.agentTarget(ctx, *board, *as)
		}
		if err != nil {
			return err
		}
		if *server != "" {
			chosen, e := a.namedServer(*server)
			if e != nil {
				return e
			}
			if chosen.URL != t.server.URL {
				return usageError("An agent stays on the server that issued its seat.", "Run aboard agents in a session for that server.")
			}
		}
		c, e := a.client(ctx, t.server, cred.Token, requestTimeout)
		if e != nil {
			return e
		}
		g, e := a.ownAgentsGroup(ctx, t.server, c)
		if e != nil {
			return e
		}
		groups = append(groups, g)
	} else {
		known, _, err := a.knownServers()
		if err != nil {
			return err
		}
		if *server != "" {
			srv, e := a.namedServer(*server)
			if e != nil {
				return e
			}
			known = []knownServer{{Name: srv.Name, URL: srv.URL}}
		}
		if len(known) == 0 {
			srv := a.localServer()
			known = []knownServer{{Name: srv.Name, URL: srv.URL}}
		}
		successes := 0
		var firstError error
		for _, k := range known {
			srv := k.ref()
			if srv.URL == a.localServer().URL {
				if _, err := a.ensureLocal(ctx); err != nil {
					return err
				}
			}
			c, e := a.humanClient(ctx, target{server: srv})
			g := ownAgentsGroup{Server: srv.URL, Agents: []ownAgentRow{}}
			if e == nil {
				g, e = a.ownAgentsGroup(ctx, srv, c)
			}
			if e != nil {
				if len(known) == 1 {
					return e
				}
				if firstError == nil {
					firstError = e
				}
				failure := asError(e)
				g.Error = &boardsFailure{Code: failure.Code, Message: failure.Message, Hint: failure.Hint}
			} else {
				successes++
			}
			groups = append(groups, g)
		}
		if successes == 0 {
			return firstError
		}
	}
	for _, g := range groups {
		fmt.Fprintf(&text, "Your agents on %s:\n", g.Server)
		if g.Error != nil {
			fmt.Fprintf(&text, "  unavailable: %s\n", g.Error.Message)
			continue
		}
		if len(g.Agents) == 0 {
			text.WriteString("  none\n")
		}
		for _, row := range g.Agents {
			fmt.Fprintf(&text, "  @%s · %s\n", row.Name, row.Board)
			if row.Location == nil {
				text.WriteString("    location not reported\n")
			} else {
				machine := "unknown machine"
				if row.Location.Machine != nil {
					machine = *row.Location.Machine
				}
				fmt.Fprintf(&text, "    %q · %q · %q\n    session %q · last active %s\n", machine, row.Location.Harness, row.Location.Folder, row.Location.SessionId, row.Location.LastActive.Format("2006-01-02 15:04:05Z07:00"))
			}
			if row.ReopenCommand != "" {
				fmt.Fprintf(&text, "    Reopen that conversation: %s\n", row.ReopenCommand)
			}
			fmt.Fprintf(&text, "    Pick it up in a session with its saved seat: %s\n", row.ResumeCommand)
		}
	}
	a.emit(struct {
		Servers []ownAgentsGroup `json:"servers"`
	}{groups}, text.String())
	return nil
}

func (a *app) ownAgentsGroup(ctx context.Context, srv serverRef, c *client) (ownAgentsGroup, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	out := ownAgentsGroup{Server: srv.URL, Agents: []ownAgentRow{}}
	r, err := c.api.ListOwnAgentsWithResponse(ctx)
	if err != nil {
		return out, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return out, apiError(r.StatusCode(), r.Body)
	}
	for _, m := range r.JSON200.Agents {
		row := ownAgentRow{
			ID: m.Id, Board: m.Board, Name: m.Name, Location: m.Location,
			ResumeCommand: "aboard resume " + shellWord(m.Name) + " --board " + shellWord(m.Board) + " --server " + shellWord(srv.URL),
		}
		if m.Location != nil {
			if h, ok := a.registry().Get(m.Location.Harness); ok {
				words := []string{}
				for _, arg := range h.Profile().Interactive.Resume {
					if arg == "{prompt}" {
						continue
					}
					words = append(words, shellWord(strings.ReplaceAll(arg, "{session}", m.Location.SessionId)))
				}
				if len(words) > 0 {
					row.ReopenCommand = "cd -- " + shellWord(m.Location.Folder) + " && " + strings.Join(words, " ")
				}
			}
		}
		out.Agents = append(out.Agents, row)
	}
	return out, nil
}
