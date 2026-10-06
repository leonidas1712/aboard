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

// runInvite creates a join code for an existing board and prints a prompt that brings
// one more agent onto it. It uses the person's login, so it refuses inside a harness
// session and hands over the command instead.
func runInvite(ctx context.Context, a *app, args []string) error {
	fs := a.flags("invite")
	boardFlag := fs.String("board", "", "the board to add an agent to")
	roleFlag := fs.String("role", "", "the role the agent joins as; default: the role the board's template invites, else member")
	ttl := fs.Duration("ttl", 0, "how long the code works, such as 2h; default 24h (168h with --server)")
	serverFlag := fs.Bool("server", false, "invite a person to the server instead of an agent to a board")
	guestFlag := fs.String("guest", "", "make a guest code that lets this person, from outside the server, onto the board once")
	if _, err := a.parse(fs, args, inviteUsage, 0, 0); err != nil {
		return err
	}
	guest := handleArg(*guestFlag)
	if *serverFlag {
		if *roleFlag != "" || *boardFlag != "" || guest != "" {
			return usageError("aboard invite --server invites a person to the whole server, so it takes no --role, --board or --guest.", inviteUsage)
		}
		return runServerInvite(ctx, a, *ttl)
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
	t, err := a.selectBoard(*boardFlag)
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
		text := fmt.Sprintf("Created a guest code for board %s: %s joins it as a guest from outside the server, once, within %s. "+
			"Anyone with the code can use it, so give it only to %s.\n\nGive this to %s, to paste into their agent's session:\n\n%s\n",
			board.Name, guest, durationText(time.Until(jc.ExpiresAt)), guest, guest, prompt)
		a.emit(struct {
			Board     string    `json:"board"`
			Role      string    `json:"role"`
			Guest     string    `json:"guest"`
			JoinLine  string    `json:"join_line"`
			Prompt    string    `json:"prompt"`
			ExpiresAt time.Time `json:"expires_at"`
		}{board.Name, jc.Role, guest, line, prompt, jc.ExpiresAt}, text)
		return nil
	}
	notice := noticeFor(board.Policy)

	text := fmt.Sprintf("Created a join code for board %s: an agent joins as %s. It works for %s, for any number of your own agents.\n",
		board.Name, jc.Role, durationText(time.Until(jc.ExpiresAt)))
	if notice != nil {
		text += notice.Message + "\n"
	}
	text += "\nPaste this into the agent's session:\n\n" + prompt + "\n"
	a.emit(struct {
		Board        string        `json:"board"`
		Role         string        `json:"role"`
		JoinLine     string        `json:"join_line"`
		Prompt       string        `json:"prompt"`
		ExpiresAt    time.Time     `json:"expires_at"`
		PolicyNotice *policyNotice `json:"policy_notice"`
	}{board.Name, jc.Role, line, prompt, jc.ExpiresAt, notice}, text)
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

// runServerInvite makes a server invite with the person's access key and prints the
// link a newcomer passes to aboard connect. The server is the one this directory's
// .aboard names, else the local server. The link holds a secret that makes a person on
// the server, so it refuses inside a harness session, where an agent would see it.
func runServerInvite(ctx context.Context, a *app, ttl time.Duration) error {
	if err := a.refuseInSession("Inviting a person to the server", "aboard invite --server"); err != nil {
		return err
	}
	srv := a.localServer()
	if p, ok, err := a.readProject(); err != nil {
		return err
	} else if ok && p.Server.URL != "" {
		srv = p.Server
	} else {
		// A machine signed in to one server and nothing else, such as a team server's
		// first admin right after aboard login, invites to that server.
		logins, err := a.readServerLogins()
		if err != nil {
			return err
		}
		if len(logins.Servers) == 1 {
			srv = a.serverRefFor(logins.Servers[0].URL)
		}
	}
	var started bool
	if srv.URL == a.localServer().URL {
		var err error
		if started, err = a.ensureLocal(ctx); err != nil {
			return err
		}
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
