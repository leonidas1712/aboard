package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// agentUsage is the usage of "aboard agent".
var agentUsage = usageOf("agent")

// runAgent runs "aboard agent remove" and "aboard agent prune", a person's commands for
// removing their agents, or as a board owner or server admin anyone's.
func runAgent(ctx context.Context, a *app, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "remove":
		return runAgentRemove(ctx, a, args)
	case "prune":
		return runAgentPrune(ctx, a, args)
	case "":
		if slices.ContainsFunc(args, isHelpFlag) {
			return a.showHelp("agent")
		}
		return usageError("Say what to do: aboard agent remove <name> or aboard agent prune.", agentUsage)
	}
	return usageError("aboard agent has no command "+strconv.Quote(sub)+".", agentUsage)
}

// agentRemoveOutput is aboard agent remove's --json output.
type agentRemoveOutput struct {
	Board *string          `json:"board"`
	Agent api.RemovedAgent `json:"agent"`
}

func runAgentRemove(ctx context.Context, a *app, args []string) error {
	fs := a.flags("agent")
	boardFlag := fs.String("board", "", "the board, by name, or by id for a private board a server admin isn't on")
	serverFlag := fs.String("server", "", "the board's server, with --board")
	pos, err := a.parse(fs, args, agentUsage, 1, 1)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(pos[0], "@")
	a.boardServerFlag = *serverFlag
	t, c, err := a.personClient(ctx, *boardFlag, "Removing an agent from a board", "aboard agent remove "+commandWord(name))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.RemoveAgentWithResponse(ctx, t.board, name, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	ra := *r.JSON200
	text := fmt.Sprintf("Removed agent %s from board %s.\n", ra.Id, ra.BoardId)
	if ra.Name != nil && ra.Board != nil {
		text = fmt.Sprintf("Removed %s from %s. Its messages stay on the board, and its sessions can't act there any more.\n", *ra.Name, *ra.Board)
	}
	a.emit(agentRemoveOutput{Board: ra.Board, Agent: ra}, text)
	return nil
}

// leaveOutput is aboard leave's --json output.
type leaveOutput struct {
	Board string           `json:"board"`
	Agent api.RemovedAgent `json:"agent"`
}

// runLeave removes the acting agent's own seat from its board, for good.
func runLeave(ctx context.Context, a *app, args []string) error {
	fs := a.flags("leave")
	as := fs.String("as", "", "the agent that leaves")
	boardFlag := fs.String("board", "", "the board, when the session has seats on several")
	if _, err := a.parse(fs, args, usageOf("leave"), 0, 0); err != nil {
		return err
	}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	r, err := c.api.LeaveAsAgentWithResponse(ctx, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	board := t.board
	if r.JSON200.Board != nil {
		board = *r.JSON200.Board
	}
	a.emit(leaveOutput{Board: board, Agent: *r.JSON200},
		fmt.Sprintf("%s left %s. Its messages stay on the board; a new agent there needs its person to add one (aboard join --board %s).\n",
			cred.Name, board, board))
	return nil
}

// agentPruneOutput is aboard agent prune's --json output.
type agentPruneOutput struct {
	Server          serverRef         `json:"server"`
	DisconnectedFor int               `json:"disconnected_for"`
	All             bool              `json:"all"`
	DryRun          bool              `json:"dry_run"`
	Agents          []api.PrunedAgent `json:"agents"`
	Kept            []string          `json:"kept"`
}

func runAgentPrune(ctx context.Context, a *app, args []string) error {
	fs := a.flags("agent")
	age := fs.String("disconnected-for", "7d", "how long an agent must have been disconnected, such as 7d, 2w or 36h; at least 1h")
	all := fs.Bool("all", false, "server admins: every agent on the server, not only yours")
	dry := fs.Bool("dry-run", false, "list the agents, and remove nothing")
	yes := fs.Bool("yes", false, "remove them without asking")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	if _, err := a.parse(fs, args, agentUsage, 0, 0); err != nil {
		return err
	}
	d, err := parseExpiry(*age)
	if err != nil || d < time.Hour {
		return usageError(fmt.Sprintf("%q is not a time of at least an hour. Use a number of days or weeks, such as 7d or 2w, or a duration such as 36h.", *age), agentUsage)
	}
	command := "aboard agent prune --disconnected-for " + *age
	if *all {
		command += " --all"
	}
	srv, _, c, err := a.peopleClient(ctx, *serverFlag, "Pruning agents", command)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	seconds := int(d / time.Second)
	prune := func(dryRun bool, ids []string) (*api.PruneResult, error) {
		body := api.PruneRequest{DisconnectedFor: seconds, All: all, DryRun: &dryRun}
		if !dryRun {
			body.Agents = &ids
		}
		r, err := c.api.PruneAgentsWithResponse(ctx, nil, body)
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, keyRejected(srv, r.StatusCode(), r.Body)
		}
		return r.JSON200, nil
	}
	preview, err := prune(true, nil)
	if err != nil {
		return err
	}
	out := agentPruneOutput{Server: srv, DisconnectedFor: seconds, All: *all, DryRun: true, Agents: preview.Agents, Kept: []string{}}
	whose := "of yours "
	if *all {
		whose = ""
	}
	if len(preview.Agents) == 0 {
		none := "None of your agents has"
		if *all {
			none = "No agent on " + srv.URL + " has"
		}
		a.emit(out, fmt.Sprintf("%s been disconnected for at least %s.\n", none, daysText(d)))
		return nil
	}
	listing := fmt.Sprintf("These agents %shave been disconnected for at least %s:\n%s", whose, daysText(d), pruneTable(preview.Agents, *all))
	if *dry {
		a.emit(out, listing)
		return nil
	}
	if !*yes {
		what := fmt.Sprintf("Pruning removes %s disconnected for at least %s: %s.", counted(len(preview.Agents), "agent"), daysText(d), pruneNames(preview.Agents))
		if !a.interactive() {
			return newError("confirmation_required", what+" It needs a yes first.",
				"Run "+command+" --yes to remove them, or --dry-run to only list them.")
		}
		_, _ = io.WriteString(a.env.Stdout, listing)
		question := map[int]string{1: "Remove it?", 2: "Remove both?"}[len(preview.Agents)]
		if question == "" {
			question = fmt.Sprintf("Remove all %d?", len(preview.Agents))
		}
		ok, err := a.asker().confirm(question, "", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	ids := make([]string, len(preview.Agents))
	for i, p := range preview.Agents {
		ids[i] = p.Id
	}
	done, err := prune(false, ids)
	if err != nil {
		return err
	}
	out.DryRun, out.Agents, out.Kept = false, done.Agents, done.Kept
	text := fmt.Sprintf("Removed %s: %s.\n", counted(len(done.Agents), "agent"), pruneNames(done.Agents))
	if len(done.Agents) == 0 {
		text = "Removed no agents.\n"
	}
	listed := map[string]api.PrunedAgent{}
	for _, p := range preview.Agents {
		listed[p.Id] = p
	}
	for _, id := range done.Kept {
		text += fmt.Sprintf("%s reconnected or changed since the list, so it stays.\n", prunedName(listed[id]))
	}
	a.emit(out, text)
	return nil
}

// prunedName is how an agent prune lists is named: its name, or its member id on a
// private board a server admin isn't on.
func prunedName(p api.PrunedAgent) string {
	if p.Name == nil {
		return p.Id
	}
	return *p.Name
}

// prunedBoard is the board an agent prune lists is on: its name, or its id.
func prunedBoard(p api.PrunedAgent) string {
	if p.Board == nil {
		return p.BoardId
	}
	return *p.Board
}

// pruneNames lists agents on one line: "claude-3 on qa-round, codex-2 on writer-review".
func pruneNames(agents []api.PrunedAgent) string {
	parts := make([]string, len(agents))
	for i, p := range agents {
		parts[i] = prunedName(p) + " on " + prunedBoard(p)
	}
	return strings.Join(parts, ", ")
}

// pruneTable lists agents one per line, in columns, with since when each has been
// disconnected and, across the server, whose each is.
func pruneTable(agents []api.PrunedAgent, all bool) string {
	nameW, boardW := 0, 0
	for _, p := range agents {
		nameW = max(nameW, len(prunedName(p)))
		boardW = max(boardW, len(prunedBoard(p)))
	}
	var b strings.Builder
	for _, p := range agents {
		fmt.Fprintf(&b, "  %-*s   on %-*s   since %s", nameW, prunedName(p), boardW, prunedBoard(p), p.DisconnectedSince.UTC().Format("2006-01-02"))
		if all {
			fmt.Fprintf(&b, "   %s's", p.Owner)
		}
		b.WriteString("\n")
	}
	return b.String()
}
