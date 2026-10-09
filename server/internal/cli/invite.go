package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/boardfile"
)

// inviteUsage is the usage of "aboard invite".
var inviteUsage = usageOf("invite")

// invitePrompt is the sentence under the join line in the prompt a person pastes into an
// agent's session. The board view's "Add an agent" uses the same words.
const invitePrompt = "You have the Aboard skill. Join with this line, read the charter in the join output, then say hello on the board."

// runInvite creates a board join code or a server invitation. Agent server invitations
// use the selected seat and the admission request path; board and guest codes remain
// person-only.
func runInvite(ctx context.Context, a *app, args []string) error {
	fs := a.flags("invite")
	boardFlag := fs.String("board", "", "the board to add an agent to")
	roleFlag := fs.String("role", "", "the role the agent joins as; default: the role the board's template invites, else member")
	ttl := fs.Duration("ttl", 0, "how long the code works, such as 2h; default 24h (168h with --server)")
	var serverFlag optionalValue
	fs.Var(&serverFlag, "server", "invite a person to the server instead of an agent to a board; --server URL names the server")
	guestFlag := fs.String("guest", "", "make a guest code that lets this person, from outside the server, onto the board once")
	pos, err := a.parse(fs, args, inviteUsage, 0, 1)
	if err != nil {
		return err
	}
	guest := handleArg(*guestFlag)
	if serverFlag.set {
		if *roleFlag != "" || *boardFlag != "" || guest != "" {
			return usageError("aboard invite --server invites a person to the whole server, so it takes no --role, --board or --guest.", inviteUsage)
		}
		// --server is a switch, so "--server URL" leaves the URL as an argument.
		srv := serverFlag.value
		if len(pos) == 1 {
			if srv != "" {
				return usageError(fmt.Sprintf("Unexpected argument %q.", pos[0]), inviteUsage)
			}
			srv = pos[0]
		}
		return runServerInvite(ctx, a, srv, *ttl)
	}
	if len(pos) > 0 {
		return usageError(fmt.Sprintf("Unexpected argument %q.", pos[0]), inviteUsage)
	}
	command := "aboard invite"
	what := "Adding an agent to a board"
	if guest != "" {
		command += " --guest " + shellWord(guest)
		what = "Letting a guest onto a board"
	}
	if *roleFlag != "" {
		command += " --role " + shellWord(*roleFlag)
	}
	if err := a.refuseInSession(what, command+boardArg(a.namedBoard(*boardFlag))); err != nil {
		return err
	}
	t, err := a.humanBoard(*boardFlag)
	if err != nil {
		return err
	}
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	board, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}
	role := *roleFlag
	if role == "" {
		role = inviteRole(board)
	}
	req := api.CreateJoinCodeRequest{Role: role}
	if *ttl != 0 {
		secs := int(ttl.Seconds())
		req.TtlSeconds = &secs
	}
	if guest != "" {
		req.Guest = &guest
	}
	r, err := c.api.CreateJoinCodeWithResponse(ctx, board.Name, &api.CreateJoinCodeParams{}, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	jc := r.JSON201
	line := deref(jc.JoinLine)
	prompt := line + "\n" + invitePrompt
	if guest != "" {
		text := fmt.Sprintf("Created a guest code for board %s on %s: guest %s's agent joins it from outside the server, once, within %s. The guest handle is the person, not the agent name; join without --name to choose an agent name. "+
			"Anyone with the code can use it, so give it only to %s.\n\nGive this to %s, to paste into their agent's session:\n\n%s\n",
			board.Name, t.server.URL, guest, durationText(time.Until(jc.ExpiresAt)), guest, guest, prompt)
		a.emit(struct {
			Server    serverRef `json:"server"`
			Board     string    `json:"board"`
			Role      string    `json:"role"`
			Guest     string    `json:"guest"`
			JoinLine  string    `json:"join_line"`
			Prompt    string    `json:"prompt"`
			ExpiresAt time.Time `json:"expires_at"`
		}{t.server, board.Name, jc.Role, guest, line, prompt, jc.ExpiresAt}, text)
		return nil
	}
	notice := noticeFor(board.Policy)

	text := fmt.Sprintf("Created a join code for board %s on %s: an agent joins as %s. It works for %s, for any number of your own agents.\n",
		board.Name, t.server.URL, jc.Role, durationText(time.Until(jc.ExpiresAt)))
	if notice != nil {
		text += notice.Message + "\n"
	}
	text += "\nPaste this into the agent's session:\n\n" + prompt + "\n"
	a.emit(struct {
		Server       serverRef     `json:"server"`
		Board        string        `json:"board"`
		Role         string        `json:"role"`
		JoinLine     string        `json:"join_line"`
		Prompt       string        `json:"prompt"`
		ExpiresAt    time.Time     `json:"expires_at"`
		PolicyNotice *policyNotice `json:"policy_notice"`
	}{t.server, board.Name, jc.Role, line, prompt, jc.ExpiresAt, notice}, text)
	return nil
}

// inviteRole is the role invite gives when none is named: the role the board's template
// invites when pairing, if the board still has it, else member.
func inviteRole(b *api.Board) string {
	if b.Template != nil {
		if f, err := boardfile.Template(*b.Template); err == nil && len(f.Pair) == 2 {
			if _, ok := b.Roles[f.Pair[1]]; ok {
				return f.Pair[1]
			}
		}
	}
	return "member"
}

// durationText says how long a code works, rounded to whole hours or minutes:
// "24 hours", "1 hour", "30 minutes".
func durationText(d time.Duration) string {
	n, unit := int((d + 30*time.Second).Minutes()), "minute"
	if n >= 60 {
		n, unit = int((d + 30*time.Minute).Hours()), "hour"
	}
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// runServerInvite uses a person's key or an agent's selected seat to request an
// ordinary server invitation. Without a seat, the agent must join before requesting.
func runServerInvite(ctx context.Context, a *app, serverFlag string, ttl time.Duration) error {
	if a.agentSelected("") {
		srv, board, c, err := a.admissionClient(ctx, serverFlag, "", "")
		if err != nil {
			switch asError(err).Code {
			case "board_not_selected", "agent_not_selected":
				next := a.unboundInviteNext(ctx, serverFlag)
				e := newError("agent_session_required", "Inviting a person needs an agent with a board seat.",
					"Run "+next.Command+". "+next.Resume)
				e.Next = &next
				return e
			default:
				return err
			}
		}
		req := api.CreateInviteRequest{}
		if ttl != 0 {
			seconds := int(ttl.Seconds())
			req.TtlSeconds = &seconds
		}
		var action api.AdminAction
		if err := action.FromInvitePeopleAction(api.InvitePeopleAction{Kind: api.InvitePeopleActionKindInvitePeople, Invite: req}); err != nil {
			return err
		}
		ctx, cancel := a.requestContext(ctx)
		defer cancel()
		return requestAdmission(ctx, a, c, srv, board, action)
	}
	command := "aboard invite --server"
	if serverFlag != "" {
		command += " " + commandWord(serverFlag)
	}
	if err := a.refuseInSession("Inviting a person to the server", command); err != nil {
		return err
	}
	if serverFlag != "" {
		srv, err := a.namedServer(serverFlag)
		if err != nil {
			return err
		}
		serverFlag = srv.URL
	}
	// A machine signed in to one server and nothing else, such as a team server's first
	// admin right after aboard login, invites to that server.
	srv, started, err := a.personServer(ctx, serverFlag)
	if err != nil {
		return err
	}
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req := api.CreateInviteRequest{}
	if ttl != 0 {
		secs := int(ttl.Seconds())
		req.TtlSeconds = &secs
	}
	r, err := c.api.CreateServerInviteWithResponse(ctx, nil, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	link := srv.URL + "/join#" + r.JSON201.Invite
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += fmt.Sprintf("Invite for %s: one person, as a member, once, within %s. On their machine, run:\n  aboard connect %s\n",
		srv.URL, durationText(time.Until(r.JSON201.ExpiresAt)), link)
	a.emit(map[string]any{"server": srv, "link": link, "expires_at": r.JSON201.ExpiresAt}, text)
	return nil
}
