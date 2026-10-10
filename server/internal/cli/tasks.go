package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

const taskUsage = "aboard task list|show|new|start|join|note|done|drop [--as AGENT] [--board NAME] [--json]"

type taskOutput struct {
	Board  string               `json:"board"`
	Task   api.Task             `json:"task"`
	Line   *api.AgentLine       `json:"line"`
	Next   *api.TaskRef         `json:"next"`
	Nudges []deliverytext.Nudge `json:"nudges"`
}

// taskClient resolves a reference only through credentials eligible for this command.
// A reference can select a session's board, but cannot grant access to another seat.
func (a *app) taskClient(ctx context.Context, board, as, selector string) (target, *client, error) {
	if !a.agentSelected(as) {
		t, err := a.personBoard(ctx, board)
		if err != nil {
			return target{}, nil, err
		}
		c, err := a.humanClient(ctx, t)
		return t, c, err
	}
	if selector == "" || board != "" {
		t, cred, err := a.agentTarget(ctx, board, as)
		if err != nil {
			return target{}, nil, err
		}
		c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
		return t, c, err
	}
	creds, err := a.readCredentials()
	if err != nil {
		return target{}, nil, err
	}
	name := strings.TrimPrefix(strings.TrimSpace(as), "@")
	if name == "" {
		name = strings.TrimPrefix(strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")), "@")
	}
	var eligible []agentCredential
	if key, ok := a.sessionKey(); ok {
		seats, err := a.sessionAgents(ctx, key)
		if err != nil {
			return target{}, nil, err
		}
		for _, seat := range seats {
			if name != "" && seat.Name != name {
				continue
			}
			if c, ok := creds.forSeat(seat); ok {
				eligible = append(eligible, c)
			}
		}
	} else {
		for _, c := range creds.Agents {
			if c.Name == name {
				eligible = append(eligible, c)
			}
		}
	}
	var foundT target
	var foundC *client
	for _, cred := range eligible {
		t := target{server: a.serverRefFor(cred.Server), board: cred.Board, source: boardFromAgent}
		c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
		if err != nil {
			return target{}, nil, err
		}
		if err := c.requireTasks(ctx); err != nil {
			return target{}, nil, err
		}
		r, err := c.api.GetTaskWithResponse(ctx, t.board, selector)
		if err != nil {
			return target{}, nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			if r.StatusCode() == 404 || r.StatusCode() == 403 || r.StatusCode() == 401 {
				continue
			}
			return target{}, nil, apiError(r.StatusCode(), r.Body)
		}
		if foundC != nil {
			return target{}, nil, newError("board_ambiguous", "That task selector matches more than one of your boards.", "Pass --board with the board you mean.")
		}
		foundT, foundC = t, c
	}
	if foundC != nil {
		return foundT, foundC, nil
	}
	if len(eligible) == 0 {
		return target{}, nil, newError("agent_not_selected", "No eligible agent seat can read that task.", "Run aboard status, or join its board with aboard join.")
	}
	return target{}, nil, newError("not_found", "No task with that reference is available on your boards.", "Run aboard task list --board <board> to see its tasks.")
}

func runTask(ctx context.Context, a *app, args []string) error {
	fs := a.flags("task")
	as := fs.String("as", "", "the agent to act as")
	board := fs.String("board", "", "the board")
	about := fs.String("about", "", "what the task is and why")
	noStart := fs.Bool("no-start", false, "open the task without picking it up")
	done := fs.Bool("done", false, "include done and cancelled tasks")
	all := fs.Bool("all", false, "show every task group")
	mine := fs.Bool("mine", false, "only tasks you own or help on")
	limit := fs.Int("limit", 0, "the most tasks to return")
	id := fs.String("task", "", "the task to update instead of your current task")
	base := fs.Int("base", 0, "the version of Where it stands to replace")
	cancelled := fs.Bool("cancelled", false, "close the task because it is no longer needed")
	reason := fs.String("reason", "", "why you are dropping your part")
	pos, err := a.parse(fs, args, taskUsage, 1, -1)
	if err != nil {
		return err
	}
	op := pos[0]
	rest := pos[1:]
	selector := *id
	switch op {
	case "list":
		if len(rest) != 0 {
			return usageError("task list takes no task reference.", taskUsage)
		}
	case "show", "start", "join":
		if len(rest) != 1 {
			return usageError("Name one task reference.", taskUsage)
		}
		selector = rest[0]
	case "new", "note", "done":
		if len(rest) == 0 {
			return usageError("Write the task's title or note.", taskUsage)
		}
	case "drop":
		if len(rest) > 1 {
			return usageError("Name at most one task to drop.", taskUsage)
		}
		if len(rest) == 1 {
			selector = rest[0]
		}
	default:
		return usageError("Use task list, show, new, start, join, note, done or drop.", taskUsage)
	}
	if *id != "" && op != "note" && op != "done" && op != "drop" {
		return usageError("--task selects the task for note, done or drop.", taskUsage)
	}
	if op == "drop" && len(rest) > 0 && *id != "" {
		return usageError("Name the task once, as a reference or with --task.", taskUsage)
	}
	if (op != "new" && (*about != "" || *noStart)) || (op != "list" && (*done || *all || *mine || *limit != 0)) || (op != "note" && *base != 0) || (op != "done" && *cancelled) || (op != "drop" && *reason != "") {
		return usageError("Use only the flags for this task command; run aboard help task.", taskUsage)
	}
	if *base < 0 {
		return usageError("--base cannot be negative.", taskUsage)
	}
	if *limit < 0 {
		return usageError("--limit can't be negative.", taskUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	t, c, err := a.taskClient(ctx, *board, *as, selector)
	if err != nil {
		return err
	}
	if err := c.requireTasks(ctx); err != nil {
		return err
	}
	if op == "list" {
		state := api.ListTasksParamsStateActive
		if *all || *done {
			state = api.ListTasksParamsStateAll
		}
		params := api.ListTasksParams{State: &state, Mine: mine}
		if *limit > 0 {
			params.Limit = limit
		}
		r, err := c.api.ListTasksWithResponse(ctx, t.board, &params)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		l := r.JSON200
		if l.Tasks == nil {
			l.Tasks = []api.Task{}
		}
		text := fmt.Sprintf("%s · %d open, %d done\n", t.board, l.Counts.Open+l.Counts.InProgress, l.Counts.Done+l.Counts.Cancelled)
		for _, group := range []struct {
			state api.TaskState
			title string
		}{{api.TaskStateOpen, "Not picked up"}, {api.TaskStateInProgress, "In progress"}, {api.TaskStateDone, "Done"}, {api.TaskStateCancelled, "Cancelled"}} {
			heading := false
			for _, task := range l.Tasks {
				if task.State != group.state {
					continue
				}
				if !heading {
					text += group.title + "\n"
					heading = true
				}
				text += fmt.Sprintf("  %s  %s", task.Ref, task.Title)
				if task.Owner != nil {
					text += " · " + task.Owner.Name
				}
				for _, helper := range task.With {
					text += ", with " + helper.Name
				}
				text += "\n"
			}
		}
		if len(l.Tasks) == 0 {
			text = t.board + " · no tasks. Open one with aboard task new \"…\"\n"
		}
		a.emit(l, text)
		return nil
	}
	if selector == "" && op != "new" {
		r, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		if r.JSON200.CurrentTask == nil {
			e := newError("no_current_task", "You have no current task on "+t.board+".", "Run aboard task list, then aboard task start <reference>.")
			params := api.ListTasksParams{State: ptrTo(api.ListTasksParamsStateActive)}
			if l, err := c.api.ListTasksWithResponse(ctx, t.board, &params); err == nil && l.JSON200 != nil {
				refs := []api.TaskRef{}
				for _, task := range l.JSON200.Tasks {
					if task.Owner != nil && task.Owner.Name == r.JSON200.Name {
						refs = append(refs, api.TaskRef{Id: task.Id, Ref: task.Ref, Title: task.Title})
					}
				}
				for _, task := range l.JSON200.Tasks {
					if task.State == api.TaskStateOpen {
						refs = append(refs, api.TaskRef{Id: task.Id, Ref: task.Ref, Title: task.Title})
						break
					}
				}
				e.Details = map[string]any{"tasks": refs}
			}
			return e
		}
		selector = r.JSON200.CurrentTask.Id
	}
	var task *api.Task
	switch op {
	case "new":
		start := a.agentSelected(*as) && !*noStart
		req := api.CreateTaskRequest{Title: strings.Join(rest, " "), Start: &start}
		if *about != "" {
			req.About = about
		}
		r, err := c.api.CreateTaskWithResponse(ctx, t.board, &api.CreateTaskParams{}, req)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON201 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON201
	case "show":
		r, err := c.api.GetTaskWithResponse(ctx, t.board, selector)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	case "start":
		r, err := c.api.StartTaskWithResponse(ctx, t.board, selector, &api.StartTaskParams{})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	case "join":
		r, err := c.api.JoinTaskWithResponse(ctx, t.board, selector, &api.JoinTaskParams{})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	case "note":
		r, err := c.api.UpdateTaskWithResponse(ctx, t.board, selector, &api.UpdateTaskParams{}, api.UpdateTaskRequest{Stands: ptrTo(strings.Join(rest, " ")), StandsBase: func() *int {
			if *base > 0 {
				return base
			}
			return nil
		}()})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	case "done":
		r, err := c.api.FinishTaskWithResponse(ctx, t.board, selector, &api.FinishTaskParams{}, api.FinishTaskRequest{Note: strings.Join(rest, " "), Cancelled: cancelled})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	case "drop":
		r, err := c.api.DropTaskWithResponse(ctx, t.board, selector, &api.DropTaskParams{}, api.DropTaskRequest{Reason: func() *string {
			if *reason != "" {
				return reason
			}
			return nil
		}()})
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		task = r.JSON200
	}
	if op == "show" {
		out := struct {
			Board   string              `json:"board"`
			Task    api.Task            `json:"task"`
			Files   []api.BoardFile     `json:"files"`
			Threads []api.ThreadSummary `json:"threads"`
		}{t.board, *task, []api.BoardFile{}, []api.ThreadSummary{}}
		threads, err := taskConversation(ctx, c, t.board, task.Id)
		if err != nil {
			return err
		}
		out.Threads = threads
		text := fmt.Sprintf("%s  %s · %s on %s\n", task.Ref, task.Title, strings.ReplaceAll(string(task.State), "_", " "), t.board)
		if task.Owner != nil {
			text += "Owner: " + task.Owner.Name + "\n"
		}
		if len(task.With) > 0 {
			names := []string{}
			for _, m := range task.With {
				names = append(names, m.Name)
			}
			text += "Helpers: " + strings.Join(names, ", ") + "\n"
		}
		if task.About != nil {
			text += fmt.Sprintf("About (by %s · %s):\n  %s\n", task.About.By.Name, agoText(task.About.At, time.Now()), task.About.Text)
		}
		if task.Stands != nil {
			text += fmt.Sprintf("Where it stands (by %s · %s · v%d):\n  %s\n", task.Stands.By.Name, agoText(task.Stands.At, time.Now()), task.Stands.Version, task.Stands.Text)
		}
		text += fmt.Sprintf("Conversation · %d: aboard read --task %s\n", len(threads), task.Ref)
		for _, th := range threads {
			text += threadLine(th, time.Now()) + "\n"
		}
		a.emit(out, text)
		return nil
	}
	out := taskOutput{Board: t.board, Task: *task, Nudges: []deliverytext.Nudge{}}
	if a.agentSelected(*as) {
		r, err := c.api.GetMeWithResponse(ctx)
		if err == nil && r.JSON200 != nil && r.JSON200.CurrentTask != nil {
			out.Line = &api.AgentLine{At: task.UpdatedAt, Kind: api.AgentLineKindWorking, SetBy: r.JSON200.Name, Source: api.AgentLineSourceTask, Task: r.JSON200.CurrentTask, Text: r.JSON200.CurrentTask.Title}
		}
	}
	working := "cleared"
	if out.Line != nil {
		working = out.Line.Text
	}
	text := fmt.Sprintf("%s on %s: %s\n", task.Ref, t.board, task.Title)
	switch op {
	case "new":
		if task.Owner == nil {
			text = "Opened " + text
		} else {
			text = task.Ref + " is yours on " + t.board + ": " + task.Title + "\n"
		}
	case "start":
		text = task.Ref + " is yours on " + t.board + ": " + task.Title + "\n"
	case "join":
		text = "Helping on " + text
		if task.Owner != nil {
			text = fmt.Sprintf("Helping on %s on %s (%s’s): %s\n", task.Ref, t.board, task.Owner.Name, task.Title)
		}
	case "note":
		text = task.Ref + " on " + t.board + " · Where it stands updated"
		if task.Stands != nil {
			text += fmt.Sprintf(" (v%d)", task.Stands.Version)
		}
		text += "\n"
	case "done":
		text = task.Ref + " " + string(task.State) + " on " + t.board + ". Working on: " + working + ".\n"
	case "drop":
		text = "Gave " + task.Ref + " back on " + t.board + "; it's not picked up. Working on: " + working + ".\n"
		if task.State != api.TaskStateOpen {
			text = "Stopped helping on " + task.Ref + " on " + t.board + ". Working on: " + working + ".\n"
		}
	}
	if op == "done" {
		r, err := c.api.ListTasksWithResponse(ctx, t.board, &api.ListTasksParams{State: ptrTo(api.ListTasksParamsStateOpen), Limit: ptrTo(1)})
		if err == nil && r.JSON200 != nil && len(r.JSON200.Tasks) > 0 {
			n := r.JSON200.Tasks[0]
			out.Next = &api.TaskRef{Id: n.Id, Ref: n.Ref, Title: n.Title}
			if !a.agentSelected(*as) {
				text += fmt.Sprintf("Next not picked up: %s %s (aboard task start %s)\n", n.Ref, n.Title, n.Ref)
			}
		}
	}
	if a.agentSelected(*as) {
		in := c.taskInbox(ctx)
		ref := delivery.AgentRef{Server: t.server.URL, Board: t.board}
		trigger := op
		if op == "new" && out.Line == nil {
			trigger = ""
		}
		out.Nudges = a.taskNudges(ctx, c, ref, in, trigger, *board != "")
		text += nudgesText(out.Nudges)
	}
	if out.Line != nil && op != "done" && op != "drop" {
		text += "Working on: " + out.Line.Text + "\n"
	}
	a.emit(out, text)
	return nil
}

func (a *app) agentTaskTarget(ctx context.Context, board, as, selector string) (target, agentCredential, error) {
	if selector == "" || !a.agentSelected(as) {
		return a.agentTarget(ctx, board, as)
	}
	t, c, err := a.taskClient(ctx, board, as, selector)
	if err != nil {
		return target{}, agentCredential{}, err
	}
	r, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return target{}, agentCredential{}, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return target{}, agentCredential{}, apiError(r.StatusCode(), r.Body)
	}
	creds, err := a.readCredentials()
	if err != nil {
		return target{}, agentCredential{}, err
	}
	for _, cred := range creds.Agents {
		if cred.Server == t.server.URL && cred.Board == t.board && ((cred.MemberID != "" && cred.MemberID == r.JSON200.Id) || (cred.MemberID == "" && cred.Name == r.JSON200.Name)) {
			return t, cred, nil
		}
	}
	return target{}, agentCredential{}, newError("agent_not_selected", "The selected task's seat has no credential on this machine.", "Join the board again with aboard join.")
}

func runBoardPrefix(ctx context.Context, a *app, board, as, prefix string) error {
	var t target
	var c *client
	var err error
	t, c, err = a.taskClient(ctx, board, as, "")
	if err != nil {
		return err
	}
	if err := c.requireTasks(ctx); err != nil {
		return err
	}
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	r, err := c.api.UpdateBoardWithResponse(ctx, t.board, &api.UpdateBoardParams{}, api.UpdateBoardRequest{TaskPrefix: &prefix})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(struct {
		Board  string  `json:"board"`
		Before *string `json:"before"`
		After  string  `json:"after"`
	}{t.board, b.TaskPrefix, prefix}, fmt.Sprintf("Tasks on %s are now %s-<n>; earlier tasks keep their ids.\n", t.board, prefix))
	return nil
}

func (c *client) requireTasks(ctx context.Context) error {
	r, err := c.api.GetInfoWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	if r.JSON200.Features == nil || !slices.Contains(*r.JSON200.Features, "tasks") {
		return newError("server_outdated", "The server at "+c.server.URL+" does not provide tasks.", "Ask the server's admin to upgrade aboard; use aboard upgrade if you run this server.")
	}
	return nil
}

func taskConversation(ctx context.Context, c *client, board, id string) ([]api.ThreadSummary, error) {
	out := []api.ThreadSummary{}
	seen := map[string]bool{}
	before := 0
	limit := 200
	for {
		params := api.ListMessagesParams{Task: &id, Limit: &limit, Newest: ptrTo(true)}
		if before > 0 {
			params.Before = &before
		}
		r, err := c.api.ListMessagesWithResponse(ctx, board, &params)
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, apiError(r.StatusCode(), r.Body)
		}
		for _, m := range r.JSON200.Messages {
			root := m.Id
			if m.ThreadRoot != nil {
				root = *m.ThreadRoot
			}
			if seen[root] {
				continue
			}
			seen[root] = true
			page, err := c.thread(ctx, m.Id)
			if err != nil {
				return nil, err
			}
			if page.Root == nil {
				continue
			}
			participants := []string{page.Root.From.Name}
			for _, reply := range page.Replies {
				if !slices.Contains(participants, reply.From.Name) {
					participants = append(participants, reply.From.Name)
				}
			}
			out = append(out, api.ThreadSummary{Root: *page.Root, Participants: participants})
		}
		if r.JSON200.PrevBefore == nil {
			break
		}
		before = *r.JSON200.PrevBefore
	}
	slices.SortFunc(out, func(a, b api.ThreadSummary) int {
		at, bt := a.Root.At, b.Root.At
		if a.Root.LastReplyAt != nil {
			at = *a.Root.LastReplyAt
		}
		if b.Root.LastReplyAt != nil {
			bt = *b.Root.LastReplyAt
		}
		return bt.Compare(at)
	})
	return out, nil
}

func cTaskFeature(ctx context.Context, a *app, t target, cred agentCredential) error {
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	return c.requireTasks(ctx)
}
