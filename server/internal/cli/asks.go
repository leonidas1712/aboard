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

const askUsage = `aboard ask [@name] QUESTION [OPTION …] [--going-with TEXT [--at TIME]] [--task ID | --no-task] [--as AGENT] [--board NAME] [--json]`

type askOutput struct {
	Message    cliMessage           `json:"message"`
	Recipients []recipientNote      `json:"recipients"`
	Nudges     []deliverytext.Nudge `json:"nudges"`
}

func (c *client) requireAsks(ctx context.Context) error {
	r, err := c.api.GetInfoWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	if r.JSON200.Features == nil || !slices.Contains(*r.JSON200.Features, "asks") {
		return newError("server_outdated", "The server does not provide asks.", "Ask its admin to upgrade aboard.")
	}
	return nil
}

func runAsk(ctx context.Context, a *app, args []string) error {
	fs := a.flags("ask")
	as := fs.String("as", "", "the agent to act as")
	board := fs.String("board", "", "the board")
	task := fs.String("task", "", "the task this ask is about")
	noTask := fs.Bool("no-task", false, "do not inherit your current task")
	going := fs.String("going-with", "", "what you will do unless told otherwise")
	at := fs.String("at", "", "when to go ahead: local HH:MM or duration")
	withdraw := fs.String("withdraw", "", "the ask to withdraw")
	open := fs.Bool("open", false, "show open asks")
	pos, err := a.parse(fs, args, askUsage, 0, -1)
	if err != nil {
		return err
	}
	if *task != "" && *noTask {
		return usageError("Use only one of --task and --no-task.", askUsage)
	}
	if *at != "" && *going == "" {
		return usageError("--at needs --going-with.", askUsage)
	}
	if (*open && (*withdraw != "" || len(pos) > 0)) || ((*open || *withdraw != "") && (*going != "" || *at != "" || *task != "" || *noTask)) {
		return usageError("Use --open or --withdraw separately from ask creation.", askUsage)
	}
	if !*open && *withdraw == "" && len(pos) == 0 {
		return usageError("Write the question.", askUsage)
	}
	if !*open && *withdraw == "" {
		count := len(pos)
		if count > 0 && strings.HasPrefix(pos[0], "@") {
			count--
		}
		if count < 1 || count > 5 {
			return usageError("Write a question and at most four options.", askUsage)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var t target
	var c *client
	if *open && !a.agentSelected(*as) && *board == "" {
		t.server, err = a.resolveServer("")
		if err == nil {
			c, err = a.humanClient(ctx, t)
		}
	} else {
		t, c, err = a.taskClient(ctx, *board, *as, *task)
	}
	if err != nil {
		return err
	}
	if err := c.requireAsks(ctx); err != nil {
		return err
	}
	if *open {
		params := api.ListAsksParams{State: ptrTo(api.ListAsksParamsStateOpen)}
		if a.agentSelected(*as) || *board != "" {
			params.Board = &t.board
		}
		params.ToMe = ptrTo(true)
		out := api.AskList{Asks: []api.Message{}}
		if a.agentSelected(*as) {
			params.ToMe = nil
			params.FromMe = ptrTo(true)
			r, e := c.api.ListAsksWithResponse(ctx, &params)
			if e != nil {
				return c.unreachable(e)
			}
			if r.JSON200 == nil {
				return apiError(r.StatusCode(), r.Body)
			}
			out = *r.JSON200
			params.FromMe = nil
			params.ToMe = ptrTo(true)
		}
		r, e := c.api.ListAsksWithResponse(ctx, &params)
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		seen := map[string]bool{}
		for _, m := range out.Asks {
			seen[m.Id] = true
		}
		for _, m := range r.JSON200.Asks {
			if !seen[m.Id] {
				out.Asks = append(out.Asks, m)
				seen[m.Id] = true
			}
		}
		out.More = out.More || r.JSON200.More
		if out.Asks == nil {
			out.Asks = []api.Message{}
		}
		text := t.board + " · open asks\n"
		if t.board == "" {
			text = t.server.URL + " · open asks\n"
		}
		for _, m := range out.Asks {
			text += deliveryText(m) + "\n"
		}
		a.emit(struct {
			Asks []cliMessage `json:"asks"`
			More bool         `json:"more"`
		}{Asks: cliMessages(out.Asks), More: out.More}, text)
		return nil
	}
	req := api.PostMessageRequest{}
	if *withdraw != "" {
		ref, err := parseMessageRef(*withdraw)
		if err != nil {
			return err
		}
		id, err := askMessageID(ctx, c, t.board, ref)
		if err != nil {
			return err
		}
		req.ReplyTo = &id
		req.Answer = &api.AnswerRequest{Withdrawn: ptrTo(true)}
		me, e := c.api.GetMeWithResponse(ctx)
		if e != nil {
			return c.unreachable(e)
		}
		if me.JSON200 == nil {
			return apiError(me.StatusCode(), me.Body)
		}
		req.To = ptrTo([]string{"@" + me.JSON200.Name})
		req.Body = strings.Join(pos, " ")
		if req.Body == "" {
			req.Body = "Withdrawing this ask."
		}
	} else {
		if strings.HasPrefix(pos[0], "@") {
			req.To = ptrTo([]string{pos[0]})
			pos = pos[1:]
		}
		if len(pos) == 0 {
			return usageError("Write the question after its recipient.", askUsage)
		}
		if len(pos) > 5 {
			return usageError("An ask takes at most four options.", askUsage)
		}
		req.Body = pos[0]
		req.Ask = &api.AskRequest{}
		if len(pos) > 1 {
			options := pos[1:]
			req.Ask.Options = &options
		}
		if *going != "" {
			req.Ask.GoingWith = going
		}
		if *at != "" {
			when, err := askTime(*at, time.Now())
			if err != nil {
				return err
			}
			req.Ask.GoingAt = &when
		}
		if *task != "" {
			req.About = ptrTo([]string{*task})
		} else if *noTask {
			req.About = ptrTo([]string{})
		}
	}
	r, err := c.api.PostMessageWithResponse(ctx, t.board, &api.PostMessageParams{}, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	m := r.JSON201
	out := askOutput{Message: cliMessage{Message: *m}, Recipients: recipientsOf(ctx, c, m), Nudges: []deliverytext.Nudge{}}
	if a.agentSelected(*as) && *withdraw == "" {
		me, e := c.api.GetMeWithResponse(ctx)
		if e == nil && me.JSON200 != nil && me.JSON200.Kind == "agent" {
			ref := delivery.AgentRef{Server: t.server.URL, Board: t.board, Name: me.JSON200.Name, MemberID: me.JSON200.Id}
			out.Nudges = a.taskNudges(ctx, c, ref, c.taskInbox(ctx), "ask", *board != "", m)
		}
	}
	text := askText(*m, *withdraw != "") + nudgesText(out.Nudges)
	a.emit(out, text+recipientsText(out.Recipients))
	return nil
}

func askTime(value string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(d), nil
	}
	if parsed, err := time.ParseInLocation("15:04", value, now.Location()); err == nil {
		return time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, now.Location()), nil
	}
	return time.Time{}, newError("ask_invalid", "Invalid --at time.", "Use local HH:MM or a duration such as 20m or 2h.")
}

// askMessageID never borrows another board's credentials to resolve a reply.
func askMessageID(ctx context.Context, c *client, board string, ref messageRef) (string, error) {
	if ref.Board != "" && ref.Board != board {
		return "", newError("message_ref_invalid", "The ask is on another board.", "Select that board with --board.")
	}
	if ref.ID != "" {
		return ref.ID, nil
	}
	page, err := c.messages(ctx, board, api.ListMessagesParams{After: ptrTo(ref.Seq - 1), Limit: ptrTo(1)})
	if err != nil {
		return "", err
	}
	if len(page.Messages) == 0 || page.Messages[0].Seq != ref.Seq {
		return "", newError("message_ref_invalid", "That ask is not available.", "Run aboard ask --open on this board.")
	}
	return page.Messages[0].Id, nil
}

func askText(m api.Message, withdraw bool) string {
	if withdraw {
		return fmt.Sprintf("Withdrew ask #%d on %s.\n", derefSeq(m.ReplyToSeq), m.Board)
	}
	if m.Ask == nil {
		return fmt.Sprintf("Asked #%d on %s\n", m.Seq, m.Board)
	}
	a := m.Ask
	text := fmt.Sprintf("Asked @%s #%d on %s", a.To.Name, m.Seq, m.Board)
	if a.Task != nil {
		text += " · about " + a.Task.Ref
	}
	text += "\n"
	if a.GoingWith != nil {
		text += fmt.Sprintf("Going with %q", *a.GoingWith)
		if a.GoingAt != nil {
			text += " at " + a.GoingAt.Format("15:04")
		}
		text += " unless @" + a.To.Name + " says otherwise. This ask blocks nothing.\n"
	} else {
		if a.Task != nil {
			text += a.Task.Ref + " is Blocked until @" + a.To.Name + " answers. "
		}
		text += "The answer wakes you: end your turn, or work on something else.\n"
	}
	return text
}

func derefSeq(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func runAskOption(ctx context.Context, a *app, board, as, task string, noTask bool, reply string, option int, body string, to []string, urgent, expect bool, wait int, boardFiles, attach []string) error {
	if wait != 0 {
		return newError("agent_not_selected", "Waiting for a reply needs an agent seat; a person can answer an option without waiting.", "Omit --wait-reply, or run the command from the selected agent session.")
	}
	if reply == "" || option < 1 || option > 4 {
		return usageError("--option takes 1 to 4 and needs --reply.", usageOf("say"))
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	t, c, err := a.taskClient(ctx, board, as, task)
	if err != nil {
		return err
	}
	if err := c.requireAsks(ctx); err != nil {
		return err
	}
	ref, err := parseMessageRef(reply)
	if err != nil {
		return err
	}
	id, err := askMessageID(ctx, c, t.board, ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		body, err = askOptionBody(ctx, c, t.board, id, option)
		if err != nil {
			return err
		}
	}
	req := api.PostMessageRequest{Body: body, ReplyTo: &id, Answer: &api.AnswerRequest{Option: &option}}
	if slices.Contains(to, "mine") {
		me, e := c.api.GetMeWithResponse(ctx)
		if e != nil {
			return c.unreachable(e)
		}
		if me.JSON200 == nil {
			return apiError(me.StatusCode(), me.Body)
		}
		for i, target := range to {
			if target == "mine" {
				to[i] = "owner:" + me.JSON200.Name
			}
		}
	}
	if len(to) > 0 {
		req.To = &to
	}
	if urgent {
		req.Urgent = &urgent
	}
	if expect {
		req.ExpectsReply = &expect
	}
	if task != "" {
		req.About = ptrTo([]string{task})
	} else if noTask {
		req.About = ptrTo([]string{})
	}
	files, err := a.sayFiles(ctx, c, t, boardFiles, attach)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		req.Files = &files
	}
	r, err := c.api.PostMessageWithResponse(ctx, t.board, &api.PostMessageParams{}, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	out := sayOutput{Message: cliMessage{Message: *r.JSON201}, Recipients: recipientsOf(ctx, c, r.JSON201), Nudges: []deliverytext.Nudge{}}
	a.emit(out, fmt.Sprintf("Sent #%d to %s on %s\n", r.JSON201.Seq, targetsText(r.JSON201.To), t.board)+recipientsText(out.Recipients))
	return nil
}

func askOptionBody(ctx context.Context, c *client, board, id string, option int) (string, error) {
	before := 0
	for {
		params := api.ListMessagesParams{Newest: ptrTo(true), Limit: ptrTo(200)}
		if before > 0 {
			params.Before = &before
		}
		page, err := c.messages(ctx, board, params)
		if err != nil {
			return "", err
		}
		for _, m := range page.Messages {
			if m.Id != id {
				continue
			}
			if m.Board != board || m.Ask == nil || option > len(m.Ask.Options) {
				return "", newError("ask_invalid", "That option is not available on this ask.", "Read the ask and choose one of its numbered options.")
			}
			return m.Ask.Options[option-1], nil
		}
		if page.PrevBefore == nil {
			break
		}
		next := *page.PrevBefore
		if next <= 0 || (before > 0 && next >= before) {
			return "", newError("internal", "The server returned an invalid message page.", "Ask the server's admin to check the message list.")
		}
		before = next
	}
	return "", newError("message_ref_invalid", "That ask is not available on this board.", "Run aboard ask --open on this board.")
}
