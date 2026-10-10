package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
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
	if len(args) > 0 && (args[0] == "list" || args[0] == "revoke") {
		return runInviteManagement(ctx, a, args[0], args[1:])
	}
	fs := a.flags("invite")
	var boards listFlag
	fs.Var(&boards, "board", "the board; repeat to bundle server-invite memberships")
	pairing := fs.String("pairing", "", "proposed work with this session on exactly one bundled board")
	roleFlag := fs.String("role", "", "the role the agent joins as; default: the role the board's template invites, else member")
	ttl := fs.Duration("ttl", 0, "how long the code works, such as 2h; default 24h for codes and agent invites, 168h for person invites")
	suggested := fs.String("handle", "", "suggested recipient handle; only with --person, never reserved")
	personFlag := fs.Bool("person", false, "invite a person to this server")
	var serverFlag optionalValue
	fs.Var(&serverFlag, "server", "select the issuer; only a bare flag retains the deprecated person-invite alias")
	guestFlag := fs.String("guest", "", "make a guest code that lets this person, from outside the server, onto the board once")
	pos, err := a.parse(fs, args, inviteUsage, 0, 1)
	if err != nil {
		return err
	}
	guest := handleArg(*guestFlag)
	if *personFlag || (serverFlag.set && serverFlag.value == "" && len(pos) == 0 && len(boards) == 0 && *guestFlag == "" && *pairing == "") {
		if *roleFlag != "" || guest != "" {
			return usageError("A person invite takes no --role or --guest; use a board code for those options.", inviteUsage)
		}
		// --server is a switch, so "--server URL" leaves the URL as an argument.
		srv := serverFlag.value
		if len(pos) == 1 {
			if srv != "" {
				return usageError(fmt.Sprintf("Unexpected argument %q.", pos[0]), inviteUsage)
			}
			srv = pos[0]
		}
		a.agentServerFlag = srv
		return runBundledServerInvite(ctx, a, srv, *ttl, boards, *pairing, handleArg(*suggested))
	}
	if *suggested != "" {
		return usageError("--handle requires --person.", inviteUsage)
	}
	if len(boards) > 1 || *pairing != "" {
		return usageError("Multiple boards and --pairing require --person.", inviteUsage)
	}
	if serverFlag.set {
		a.boardServerFlag = serverFlag.value
		if len(pos) == 1 && a.boardServerFlag == "" {
			a.boardServerFlag = pos[0]
			pos = nil
		}
	}
	boardFlag := ""
	if len(boards) == 1 {
		boardFlag = boards[0]
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
	if *ttl != 0 {
		command += " --ttl " + commandWord(ttl.String())
	}
	if err := a.refuseInSession(what, command+a.boardFlags(boardFlag)); err != nil {
		return err
	}
	t, err := a.humanBoard(ctx, boardFlag)
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
func runBundledServerInvite(ctx context.Context, a *app, serverFlag string, ttl time.Duration, boards []string, pairing, suggested string) error {
	if pairing != "" && len(boards) != 1 {
		return usageError("--pairing requires exactly one --board.", inviteUsage)
	}

	if a.agentSelected("") {
		selectedBoard := ""
		if len(boards) == 1 && !strings.HasPrefix(boards[0], "brd_") {
			selectedBoard = boards[0]
		}
		srv, board, c, err := a.admissionClient(ctx, serverFlag, selectedBoard, "")
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
		if suggested != "" {
			req.SuggestedHandle = &suggested
		}
		if ttl != 0 {
			seconds := int(ttl.Seconds())
			req.TtlSeconds = &seconds
		}
		ctx, cancel := a.requestContext(ctx)
		defer cancel()
		if err := a.bundleInvite(ctx, c, &req, boards, pairing); err != nil {
			return err
		}
		var action api.AdminAction
		if err := action.FromInvitePeopleAction(api.InvitePeopleAction{Kind: api.InvitePeopleActionKindInvitePeople, Invite: req}); err != nil {
			return err
		}
		if pairing != "" {
			return requestAdmission(ctx, a, c, srv, board, action, func(result *api.AdminActionResult) { a.selectInvitedPairing(ctx, srv, result) })
		}
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
	if suggested != "" {
		req.SuggestedHandle = &suggested
	}
	if ttl != 0 {
		secs := int(ttl.Seconds())
		req.TtlSeconds = &secs
	}
	if err := a.bundleInvite(ctx, c, &req, boards, pairing); err != nil {
		return err
	}
	r, err := c.api.CreateServerInviteWithResponse(ctx, nil, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	link, prompt := inviteHandover(srv, *r.JSON201)
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += fmt.Sprintf("Invite for %s: one person, as a member, once, within %s. On their machine, run:\n  aboard connect %s\n",
		srv.URL, durationText(time.Until(r.JSON201.ExpiresAt)), link)
	text += "\n" + prompt + "\n"
	out := map[string]any{"server": srv, "link": link, "expires_at": r.JSON201.ExpiresAt, "prompt": prompt}
	if r.JSON201.SuggestedHandle != nil {
		out["suggested_handle"] = *r.JSON201.SuggestedHandle
		text = "Invite for @" + *r.JSON201.SuggestedHandle + "\n" + text
	}
	if r.JSON201.Boards != nil {
		out["boards"] = r.JSON201.Boards
	}
	if r.JSON201.PairingRequestId != nil {
		out["pairing_request_id"] = r.JSON201.PairingRequestId
	}
	a.emit(out, text)
	return nil
}

func (a *app) selectInvitedPairing(ctx context.Context, srv serverRef, result *api.AdminActionResult) {
	if result.Invite == nil || result.Invite.PairingRequestId == nil || *result.Invite.PairingRequestId == "" {
		return
	}
	id := *result.Invite.PairingRequestId
	next := invitedPairingNext(srv, id)
	var captured bytes.Buffer
	worker := *a
	worker.json = true
	worker.env.Stdout = &captured
	worker.env.Stderr = io.Discard
	if err := runPairing(ctx, &worker, []string{"select", id, "--here", "--server", srv.URL}); err != nil {
		result.Next = next
		return
	}
	result.Next = nil
	var selected struct {
		Server  serverRef          `json:"server"`
		Request api.PairingRequest `json:"request"`
	}
	if json.Unmarshal(captured.Bytes(), &selected) != nil || selected.Server.URL != srv.URL || selected.Request.Id != id || selected.Request.ServerId == "" {
		result.Next = next
	}
}

func invitedPairingNext(srv serverRef, id string) *api.NextStep {
	return &api.NextStep{Command: "aboard pairing select " + commandWord(id) + " --here --server " + commandWord(srv.URL), Resume: "The invite was issued. Continue this same pairing in its original initiating session; do not create another invite."}
}

func serverInvitePrompt(link string, pairing bool, suggested ...string) string {
	handle := "<name you'd like teammates to see>"
	if len(suggested) > 0 && suggested[0] != "" {
		handle = suggested[0]
	}
	prompt := "Install Aboard with curl -fsSL https://comeaboard.dev/install | sh, run aboard skill, then run aboard setup " + link + " --handle " + handle + "."
	if pairing {
		prompt += " Verify you can exchange messages with the inviting agent."
	}
	return prompt
}

func (a *app) bundleInvite(ctx context.Context, c *client, req *api.CreateInviteRequest, selectors []string, work string) error {
	ids := []string{}
	var chosen *api.Board
	for _, selector := range selectors {
		board, err := c.board(ctx, selector)
		if err != nil {
			return err
		}
		if board.Id == "" {
			return newError("internal", "The server did not return this board's permanent identity.", "Ask the admin to upgrade the server.")
		}
		if !slices.Contains(ids, board.Id) {
			ids = append(ids, board.Id)
		}
		chosen = board
	}
	if len(ids) > 0 {
		req.Boards = &ids
	}
	if work == "" {
		return nil
	}
	_, ok, err := a.checkSession(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return newError("agent_session_required", "A pairing proposal needs its initiating session.", "Run this invitation command inside the agent session that will pair.")
	}
	_, cred, err := a.agentTarget(ctx, chosen.Name, "")
	if err != nil {
		return err
	}
	if cred.Server != c.server.URL || cred.Board != chosen.Name {
		return newError("agent_not_selected", "The initiating seat belongs to another board or server.", "Run this invitation in the intended board's session.")
	}
	me, err := c.api.GetMeWithResponse(ctx)
	if err != nil {
		return c.unreachable(err)
	}
	if me.JSON200 == nil {
		return apiError(me.StatusCode(), me.Body)
	}
	if me.JSON200.Kind != api.MeKindAgent || me.JSON200.Id != cred.MemberID || me.JSON200.Board == nil || *me.JSON200.Board != chosen.Name {
		return newError("agent_not_selected", "The initiating seat could not be verified.", "Resume the intended agent and retry this invitation.")
	}
	req.Pairing = &api.InvitePairing{InitiatingAgentId: cred.MemberID, Work: work}
	return nil
}

func runInviteManagement(ctx context.Context, a *app, action string, args []string) error {
	fs := a.flags("invite")
	server := fs.String("server", "", "the invite issuer")
	board, as := "", ""
	if action == "list" {
		fs.StringVar(&board, "board", "", "select the current agent’s board seat")
		fs.StringVar(&as, "as", "", "read through this agent’s seat")
	}
	count := 0
	if action == "revoke" {
		count = 1
	}
	pos, err := a.parse(fs, args, inviteUsage, count, count)
	if err != nil {
		return err
	}
	command := "aboard invite " + action
	if len(pos) > 0 {
		command += " " + commandWord(pos[0])
	}
	if *server != "" {
		command += " --server " + commandWord(*server)
	}
	var srv serverRef
	var c *client
	selectedBoard := ""
	if action == "list" {
		srv, _, selectedBoard, c, err = a.peopleReadClient(ctx, *server, board, as)
	} else {
		if e := a.refuseInSession("Managing server invitations", command); e != nil {
			refusal := asError(e)
			refusal.Next = &api.NextStep{Command: command, Resume: "Your person runs this command in their terminal."}
			return refusal
		}
		srv, _, err = a.personServer(ctx, *server)
		if err == nil {
			c, err = a.keysClient(ctx, srv)
		}
	}
	if err != nil {
		return err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	if action == "list" {
		r, err := c.api.ListServerInvitesWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return a.peopleReadError(srv, r.StatusCode(), r.Body, as)
		}
		text := "Invitations on " + srv.URL + "\n"
		for _, invite := range r.JSON200.Invites {
			issuer := ""
			if invite.SuggestedHandle != nil {
				issuer += " · for @" + *invite.SuggestedHandle
			}
			if invite.IssuingAgentId != nil {
				name := *invite.IssuingAgentId
				if invite.Display != nil && invite.Display.AgentName != nil {
					name = "@" + *invite.Display.AgentName
				}
				issuer += " · issued by agent " + name
			}
			text += fmt.Sprintf("%s · %s%s · expires %s\n", invite.Id, invite.State, issuer, invite.ExpiresAt.Format(time.RFC3339))
		}
		out := map[string]any{"server": srv, "invites": r.JSON200.Invites}
		if selectedBoard != "" {
			out["board"] = selectedBoard
			text = "Board: " + selectedBoard + "\n" + text
		}
		a.emit(out, text)
		return nil
	}
	r, err := c.api.RevokeServerInviteWithResponse(ctx, pos[0], nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(map[string]any{"server": srv, "id": r.JSON200.Id, "revoked": r.JSON200.Revoked, "changed": r.JSON200.Changed}, "Revoked invitation "+r.JSON200.Id+" on "+srv.URL+".\n")
	return nil
}
