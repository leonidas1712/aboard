package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

var (
	allowanceUsage = usageOf("allowance")
	approvalsUsage = usageOf("approvals")
)

// admissionClient keeps agent requests on their selected seat's issuer.
func (a *app) admissionClient(ctx context.Context, server, board, as string) (serverRef, string, *client, error) {
	if a.agentSelected(as) {
		t, seat, err := a.agentTarget(ctx, board, as)
		if err != nil {
			return serverRef{}, "", nil, err
		}
		if server != "" {
			selected, err := a.namedServer(server)
			if err != nil {
				return serverRef{}, "", nil, err
			}
			if selected.URL != t.server.URL {
				return serverRef{}, "", nil, newError("agent_not_selected", "The selected seat belongs to another server.", "Use --server "+commandWord(t.server.URL)+" for this seat.")
			}
		}
		c, err := a.client(ctx, t.server, seat.Token, requestTimeout)
		return t.server, t.board, c, err
	}
	srv, _, err := a.personServer(ctx, server)
	if err != nil {
		return serverRef{}, "", nil, err
	}
	c, err := a.keysClient(ctx, srv)
	return srv, "", c, err
}

func runAllowance(ctx context.Context, a *app, args []string) error {
	fs := a.flags("allowance")
	server := fs.String("server", "", "the server whose allowance to read or change")
	board := fs.String("board", "", "select this board's agent seat")
	as := fs.String("as", "", "select this agent")
	pos, err := a.parse(fs, args, allowanceUsage, 0, 3)
	if err != nil {
		return err
	}
	if len(pos) == 1 && pos[0] == "get" {
		pos = nil
	}
	validToggle := len(pos) == 1 && (pos[0] == "on" || pos[0] == "off")
	validCategory := len(pos) == 3 && pos[0] == "set" && (pos[1] == "invite-people" || pos[1] == "add-people") && (pos[2] == "on" || pos[2] == "off")
	if len(pos) > 0 && !validToggle && !validCategory {
		return usageError("Use allowance, allowance on|off, or allowance set invite-people|add-people on|off.", allowanceUsage)
	}
	srv, _, c, err := a.admissionClient(ctx, *server, *board, *as)
	if err != nil {
		return err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	var out *api.Allowance
	if len(pos) == 0 || len(pos) == 3 {
		r, e := c.api.GetAllowanceWithResponse(ctx)
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		out = r.JSON200
	}
	if len(pos) > 0 {
		cats := []api.AllowanceCategory{}
		if len(pos) == 1 && pos[0] == "on" {
			cats = []api.AllowanceCategory{api.AllowanceCategoryAddPeople}
		}
		if len(pos) == 3 {
			cats = append(cats, out.Categories...)
			category := api.AllowanceCategory(pos[1])
			cats = slices.DeleteFunc(cats, func(c api.AllowanceCategory) bool { return c == category })
			if pos[2] == "on" {
				cats = append(cats, category)
			}
		}
		r, e := c.api.SetAllowanceWithResponse(ctx, nil, api.SetAllowanceRequest{Categories: cats})
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		out = r.JSON200
	}
	names := make([]string, len(out.Categories))
	for i, c := range out.Categories {
		names[i] = string(c)
	}
	state := "off"
	if len(names) > 0 {
		state = strings.Join(names, ", ")
	}
	text := fmt.Sprintf("Allowance on %s: %s\n", srv.URL, state)
	if out.Warning != nil {
		text += *out.Warning + "\n"
	}
	a.emit(map[string]any{"server": srv, "allowance": out}, text)
	return nil
}

func runApprovals(ctx context.Context, a *app, args []string) error {
	fs := a.flags("approvals")
	server := fs.String("server", "", "the issuer of these approval ids")
	board := fs.String("board", "", "select this board's agent seat")
	as := fs.String("as", "", "select this agent")
	always := fs.Bool("always", false, "also allow this action's category in future")
	pos, err := a.parse(fs, args, approvalsUsage, 0, 2)
	if err != nil {
		return err
	}
	if len(pos) == 1 && pos[0] == "list" {
		pos = nil
	}
	validDecision := len(pos) == 2 && (pos[0] == "allow" || pos[0] == "decline")
	validShow := len(pos) == 2 && pos[0] == "show"
	if len(pos) > 0 && !validDecision && !validShow {
		return usageError("Use approvals, approvals show ID, approvals allow ID, or approvals decline ID.", approvalsUsage)
	}
	if *always && (len(pos) != 2 || pos[0] != "allow") {
		return usageError("--always requires approvals allow ID.", approvalsUsage)
	}
	srv, seatBoard, c, err := a.admissionClient(ctx, *server, *board, *as)
	if err != nil {
		return err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	if validShow {
		return a.showApproval(ctx, c, srv, seatBoard, pos[1], a.agentSelected(*as))
	}
	if len(pos) == 0 {
		r, e := c.api.ListApprovalsWithResponse(ctx, nil)
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		approvals := r.JSON200.Approvals
		slices.SortStableFunc(approvals, func(x, y api.Approval) int {
			if x.State == y.State {
				return 0
			}
			if x.State == api.ApprovalStatePending {
				return -1
			}
			if y.State == api.ApprovalStatePending {
				return 1
			}
			return 0
		})
		var text strings.Builder
		fmt.Fprintf(&text, "Approvals on %s", srv.URL)
		if seatBoard != "" {
			fmt.Fprintf(&text, " · %s", seatBoard)
		}
		text.WriteString("\n")
		for _, v := range approvals {
			kind, agent, target := approvalDisplay(v)
			fmt.Fprintf(&text, "%s · %s · %s%s · %s\n", v.Id, kind, agent, target, v.State)
		}
		out := map[string]any{"server": srv, "approvals": approvals}
		if seatBoard != "" {
			out["board"] = seatBoard
		}
		a.emit(out, text.String())
		return nil
	}
	if pos[0] == "allow" {
		r, e := c.api.AllowApprovalWithResponse(ctx, pos[1], nil, api.AllowApprovalRequest{Always: always})
		if e != nil {
			return c.unreachable(e)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		return emitAdmissionResult(a, srv, "", r.JSON200)
	}
	r, e := c.api.DeclineApprovalWithResponse(ctx, pos[1], nil)
	if e != nil {
		return c.unreachable(e)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	a.emit(map[string]any{"server": srv, "approval": r.JSON200}, fmt.Sprintf("Declined %s on %s.\n", r.JSON200.Id, srv.URL))
	return nil
}

func emitAdmissionResult(a *app, srv serverRef, board string, result *api.AdminActionResult) error {
	out := map[string]any{"server": srv, "approval": result.Approval}
	if board != "" {
		out["board"] = board
	}
	if result.Invite != nil {
		out["invite"] = result.Invite
	}
	if result.Next != nil {
		out["next"] = result.Next
	}
	text := fmt.Sprintf("%s · %s on %s", result.Approval.Id, result.State, srv.URL)
	if board != "" {
		text += " · " + board
	}
	text += "\n"
	if result.Warning != nil {
		out["warning"] = *result.Warning
		text += *result.Warning + "\n"
	}
	if result.Invite != nil && result.Invite.Invite != "" {
		text += "Invite: aboard connect " + commandWord(srv.URL+"/join#"+result.Invite.Invite) + "\n"
		prompt := serverInvitePrompt(srv.URL+"/join#"+result.Invite.Invite, result.Invite.PairingRequestId != nil, deref(result.Invite.SuggestedHandle))
		out["prompt"] = prompt
		text += prompt + "\n"
	}
	if result.Next != nil {
		text += result.Next.Command + "\n" + result.Next.Resume + "\n"
	}
	a.emit(out, text)
	return nil
}

func requestAdmission(ctx context.Context, a *app, c *client, srv serverRef, board string, action api.AdminAction, afterExecution ...func(*api.AdminActionResult)) error {
	r, err := c.api.RequestAdminActionWithResponse(ctx, nil, action)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON202 != nil {
		held := r.JSON202
		if err := a.watchRequestedApproval(ctx, srv, held.Approval); err != nil {
			held.Next.Resume += " The next-turn notice could not be registered. Check this approval with aboard approvals show " + commandWord(held.Approval.Id) + " --server " + commandWord(srv.Name) + " --board " + commandWord(board) + "; do not request another invite."
		}
		out := map[string]any{"server": srv, "state": "pending", "approval": held.Approval, "next": held.Next}
		if board != "" {
			out["board"] = board
		}
		text := "Pending approval " + held.Approval.Id + " on " + srv.URL
		if board != "" {
			text += " · " + board
		}
		text += "\n" + held.Next.Command + "\n"
		if held.Next.BoardView != nil {
			text += *held.Next.BoardView + "\n"
		}
		text += held.Next.Resume + "\n"
		a.emit(out, text)
		return nil
	}
	result := r.JSON200
	if result == nil {
		result = r.JSON201
	}
	if result == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	for _, complete := range afterExecution {
		complete(result)
	}
	return emitAdmissionResult(a, srv, board, result)
}

func (a *app) watchRequestedApproval(ctx context.Context, srv serverRef, approval api.Approval) error {
	key, ok := a.sessionKey()
	if !ok {
		return newError("agent_session_required", "There is no originating session for an approval notice.", "Check this approval using its exact requesting seat.")
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	for _, seat := range creds.Agents {
		if seat.Server != srv.URL || seat.MemberID != approval.AgentId {
			continue
		}
		ref := delivery.AgentRef{Server: srv.URL, Board: seat.Board, Name: seat.Name, MemberID: seat.MemberID}
		_, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpApprovalWatch, Harness: key.Harness, Session: key.ID, Boot: a.env.Getenv("ABOARD_BOOT"), Server: srv.URL, ApprovalID: approval.Id, Agent: &ref})
		return err
	}
	return newError("agent_not_selected", "The approval's requesting seat is unavailable.", "Use the exact seat that requested this approval.")
}

func approvalDisplay(v api.Approval) (kind, agent, target string) {
	var action struct {
		Kind string `json:"kind"`
	}
	kind = "action"
	if data, err := v.Action.MarshalJSON(); err == nil && json.Unmarshal(data, &action) == nil && action.Kind != "" {
		kind = strings.ReplaceAll(action.Kind, "_", " ")
	}
	agent = v.AgentId
	if v.Display == nil {
		return
	}
	d := v.Display
	if d.AgentName != nil {
		agent = "@" + *d.AgentName
	}
	if d.TargetHandle != nil {
		target += " · @" + *d.TargetHandle
	}
	if d.KeyName != nil {
		target += " · key " + *d.KeyName
	}
	for _, b := range d.Boards {
		target += " · board " + b.Name
	}
	if len(d.Boards) == 0 && d.RequestedOn != nil {
		target += " · board " + d.RequestedOn.Name
	}
	return
}
