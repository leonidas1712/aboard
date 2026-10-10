package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

const pairingUsage = "aboard pairing [list]|request @person|me WORK|accept [ID] --here|select ID --here|decline ID|cancel ID [--board BOARD] [--server SERVER] [--json]"

func runPairing(ctx context.Context, a *app, args []string) error {
	fs := a.flags("pairing")
	board := fs.String("board", "", "the request's board")
	as := fs.String("as", "", "the initiating agent")
	server := fs.String("server", "", "the request's server")
	here := fs.Bool("here", false, "accept in this exact harness session")
	replace := fs.Bool("replace", false, "explicitly replace this side's endpoint")
	pos, err := a.parse(fs, args, pairingUsage, 0, -1)
	if err != nil {
		return err
	}
	action, id := "list", ""
	if len(pos) > 0 {
		action = pos[0]
		pos = pos[1:]
	}
	switch action {
	case "list":
		if len(pos) != 0 {
			return usageError("List does not take a request id.", pairingUsage)
		}
	case "request":
		if len(pos) != 2 {
			return usageError("Name the person and quote the proposed work.", pairingUsage)
		}
	case "accept", "select":
		if len(pos) > 1 || !*here || (action == "select" && len(pos) != 1) {
			return usageError("Accept needs --here and at most one request id.", pairingUsage)
		}
		if len(pos) == 1 {
			id = pos[0]
		}
	case "decline", "cancel":
		if len(pos) != 1 {
			return usageError("Name one pairing request.", pairingUsage)
		}
		id = pos[0]
	default:
		return usageError("Choose list, request, select, accept, decline or cancel.", pairingUsage)
	}
	if (*here || *replace) && action != "accept" && action != "select" {
		return usageError("--here and --replace belong to accept or select.", pairingUsage)
	}
	if (action == "accept" || action == "select") && (*as != "" || *board != "") {
		return usageError("Endpoint selection uses this exact session and the request id; do not pass --as or --board.", pairingUsage)
	}
	a.agentServerFlag = *server
	key, inSession, err := a.checkSession(ctx)
	if err != nil {
		return err
	}
	if (action == "accept" || action == "select") && !inSession {
		return pairingNeedsSession()
	}
	var srv serverRef
	var c *client
	var create api.CreatePairingRequest
	switch {
	case action == "request":
		if !a.agentSelected(*as) {
			return newError("agent_not_selected", "Choose the agent that starts this pairing.", "Run this command in that agent's session, or name its seat with --as and --board.")
		}
		t, cred, err := a.agentTarget(ctx, *board, *as)
		if err != nil {
			return err
		}
		srv = t.server
		if *server != "" {
			explicit, err := a.namedServer(*server)
			if err != nil {
				return err
			}
			if explicit.URL != srv.URL {
				return newError("session_on_another_server", "The selected seat belongs to another server.", "Use its issuer server, or select a seat on the requested server.")
			}
		}
		c, err = a.client(ctx, srv, cred.Token, requestTimeout)
		if err != nil {
			return err
		}
		b, err := c.api.GetBoardWithResponse(ctx, t.board)
		if err != nil {
			return c.unreachable(err)
		}
		if b.JSON200 == nil {
			return apiError(b.StatusCode(), b.Body)
		}
		me, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if me.JSON200 == nil {
			return apiError(me.StatusCode(), me.Body)
		}
		name := strings.TrimPrefix(pos[0], "@")
		if name == "me" && me.JSON200.Owner != nil {
			name = *me.JSON200.Owner
		}
		lookup, err := c.api.ListServerPeopleWithResponse(ctx, &api.ListServerPeopleParams{Handle: &name})
		if err != nil {
			return c.unreachable(err)
		}
		if lookup.JSON200 == nil {
			return apiError(lookup.StatusCode(), lookup.Body)
		}
		person, err := lookup.JSON200.AsPersonIdentityLookup()
		if err != nil {
			return err
		}
		personID := person.Id
		create = api.CreatePairingRequest{BoardId: b.JSON200.Id, RecipientId: personID, InitiatingAgentId: me.JSON200.Id, Work: pos[1]}
	case inSession:
		seats, err := a.sessionAgents(ctx, key)
		if err != nil {
			return err
		}
		seatServer, err := sessionIssuer(seats, *server)
		if err != nil {
			return err
		}
		srv, err = a.boardServer(ctx, *server, seatServer)
		if err != nil {
			return err
		}
	default:
		if a.agentSelected(*as) {
			t, cred, err := a.agentTarget(ctx, *board, *as)
			if err != nil {
				return err
			}
			srv = t.server
			if *server != "" {
				explicit, err := a.namedServer(*server)
				if err != nil {
					return err
				}
				if explicit.URL != srv.URL {
					return newError("session_on_another_server", "The selected seat belongs to another server.", "Use the selected seat's server.")
				}
			}
			c, err = a.client(ctx, srv, cred.Token, requestTimeout)
			if err != nil {
				return err
			}
		} else {
			srv, err = a.approveServer(ctx, *server)
			if err != nil {
				return err
			}
			c, err = a.humanClient(ctx, target{server: srv})
			if err != nil {
				return err
			}
		}
	}
	if inSession {
		op, err := idempotencyKey(a.env.Rand)
		if err != nil {
			return err
		}
		req := delivery.Request{Op: delivery.OpPairing, Harness: key.Harness, Session: key.ID, Server: srv.URL, PairingAction: action, PairingID: id, Replace: *replace, IdempotencyKey: op, BoardID: create.BoardId, RecipientID: create.RecipientId, InitiatingAgentID: create.InitiatingAgentId, Work: create.Work}
		resp, err := a.callDaemon(ctx, req)
		if err != nil {
			if action == "request" {
				var e *Error
				if errors.As(err, &e) && (e.Code == "server_unreachable" || e.Code == "daemon_not_running" || e.Code == "internal") {
					e.Hint = "Read aboard pairing list --server " + shellWord(srv.URL) + " before making another request. If it committed, select that request explicitly with aboard pairing select ID --here."
				}
			}
			return err
		}
		if action == "list" {
			var list api.PairingRequests
			if err := decodePairingMetadata(map[string]any{"requests": resp.Pairings}, &list); err != nil {
				return fmt.Errorf("read pairing list: %w", err)
			}
			return a.emitPairingList(srv, list)
		}
		var request api.PairingRequest
		if err := decodePairingMetadata(resp.Pairing, &request); err != nil {
			return fmt.Errorf("read pairing request: %w", err)
		}
		return a.emitPairing(srv, resp.PairingBoard, request)
	}
	return a.runPairingAPI(ctx, c, srv, action, id, create)
}

func (a *app) runPairingAPI(ctx context.Context, c *client, srv serverRef, action, id string, create api.CreatePairingRequest) error {
	switch action {
	case "list":
		r, err := c.api.ListPairingRequestsWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		return a.emitPairingList(srv, *r.JSON200)
	case "request":
		r, err := c.api.CreatePairingRequestWithResponse(ctx, nil, create)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON201 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		return a.emitPairingAPI(ctx, c, srv, *r.JSON201)
	case "decline":
		r, err := c.api.DeclinePairingRequestWithResponse(ctx, id, nil)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		return a.emitPairingAPI(ctx, c, srv, *r.JSON200)
	case "cancel":
		r, err := c.api.CancelPairingRequestWithResponse(ctx, id, nil)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return apiError(r.StatusCode(), r.Body)
		}
		return a.emitPairingAPI(ctx, c, srv, *r.JSON200)
	}
	return pairingNeedsSession()
}

func (a *app) emitPairingList(srv serverRef, list api.PairingRequests) error {
	if list.Requests == nil {
		list.Requests = []api.PairingRequest{}
	}
	text := "No pairing requests.\n"
	if len(list.Requests) > 0 {
		var b strings.Builder
		for _, r := range list.Requests {
			fmt.Fprintf(&b, "%s · board %s · %s\n", r.Id, r.BoardId, r.State)
		}
		text = b.String()
	}
	a.emit(struct {
		Server   serverRef            `json:"server"`
		Requests []api.PairingRequest `json:"requests"`
	}{srv, list.Requests}, text)
	return nil
}

func (a *app) emitPairing(srv serverRef, board string, r api.PairingRequest) error {
	text := fmt.Sprintf("%s · board %s · %s\n", r.Id, board, r.State)
	if r.Next != nil {
		text += r.Next.Command + "\n"
	}
	a.emit(struct {
		Server  serverRef          `json:"server"`
		Board   string             `json:"board"`
		Request api.PairingRequest `json:"request"`
		Next    *api.NextStep      `json:"next,omitempty"`
	}{srv, board, r, r.Next}, text)
	return nil
}

func decodePairingMetadata(value, target any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func (a *app) emitPairingAPI(ctx context.Context, c *client, srv serverRef, request api.PairingRequest) error {
	r, err := c.api.GetBoardWithResponse(ctx, request.BoardId)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	return a.emitPairing(srv, r.JSON200.Name, request)
}

func pairingNeedsSession() error {
	return newError("agent_session_required", "Pairing acceptance needs the exact harness session that will take part.", "Run aboard pairing accept REQUEST_ID --here inside that session.")
}

func (a *app) unboundInviteNext(ctx context.Context, serverFlag string) api.NextStep {
	command := "aboard boards"
	issuer := serverFlag
	if issuer == "" {
		if project, ok, err := a.readProject(); err == nil && ok {
			issuer = project.Server.URL
		}
	}
	if issuer != "" {
		command += " --server " + shellWord(issuer)
	}
	next := api.NextStep{Command: command, Resume: "Join one with aboard join --board NAME in this session, then retry the invitation."}
	key, inSession, err := a.checkSession(ctx)
	if err != nil || !inSession {
		return next
	}
	srv, err := a.boardServer(ctx, issuer, "")
	if err != nil {
		return next
	}
	response, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpPairing, Harness: key.Harness, Session: key.ID, Server: srv.URL, PairingAction: "list"})
	if err != nil || response.Error != nil {
		return next
	}
	for _, request := range response.Pairings {
		switch request.State {
		case "ready", "declined", "cancelled", "expired":
			continue
		default:
			return api.NextStep{Command: "aboard pairing list --server " + shellWord(srv.URL), Resume: "Choose the request in this exact session, then retry the invitation after pairing is ready."}
		}
	}
	return next
}
