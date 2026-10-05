package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// A session finds and joins its person's boards through the machine's delegation, which
// the daemon holds for each server (spec/control.md, "The machine's delegation"). The
// daemon vouches for the session (it runs on this machine, under this person's login),
// asks the server, which alone decides whether the session gets a new seat or its
// earlier one, saves the seat's token before anything else, and only then binds it. It
// never answers a join from its own records, and on any failure it binds nothing.

// Seats is how the daemon reaches a server through the machine's delegation, and where
// it keeps the tokens of the seats it is given.
type Seats interface {
	// Boards lists the boards the person can see on server. ErrLoginMissing means this
	// machine has no key for it; ErrServerUnreachable and ErrServerOutdated as for Join;
	// a server's refusal is a *WireError, passed on as it is.
	Boards(ctx context.Context, server string) ([]SeatBoard, error)
	// Join asks server for the session's seat on a board. Errors are as for Boards; a
	// lost answer is ErrServerUnreachable, never a seat.
	Join(ctx context.Context, server string, req SeatRequest) (SeatGrant, error)
	// Save writes a seat's token where the session's commands find it, whole or not at
	// all, replacing the seat's earlier token.
	Save(ctx context.Context, seat SeatRef, token string) error
	// SeatID returns the member id this machine keeps for an agent, if any.
	SeatID(agent AgentRef) (string, bool)
}

// SeatBoard is one board a delegation lists: its name, and the server's Board for it.
type SeatBoard struct {
	Name  string
	Board json.RawMessage
}

// SeatRequest is a delegated join: the board, and optionally the role and name; Harness
// and Session (`<harness>:<id>`) are the session the daemon vouches for.
type SeatRequest struct {
	Board, Role, Name, Harness, Session string
}

// SeatGrant is what the server gave the session: the seat, its new token, whether it is
// the session's earlier seat, the server's Board and Member for it, and its delivery mode.
type SeatGrant struct {
	Seat   SeatRef
	Token  string
	Reused bool
	Board  json.RawMessage
	Member json.RawMessage
	Mode   Mode
}

var (
	// ErrServerUnreachable means the server couldn't be reached, failed (a 5xx), or its
	// answer was lost.
	ErrServerUnreachable = errors.New("the server can't be reached")
	// ErrServerOutdated means the server has no machine delegations.
	ErrServerOutdated = errors.New("the server has no machine delegations")
)

// seatsCheck checks the session a boards or join request is for, as agents does: a
// harness the daemon knows, a session it registered or its harness adapter confirms,
// and not a subagent. It returns the error response, if any.
func (d *Daemon) seatsCheck(ctx context.Context, req Request) (Response, bool) {
	ad, ok := d.adapters[req.Harness]
	if !ok || req.Session == "" {
		return errorResponse("invalid_request", fmt.Sprintf("%q is not a harness the delivery daemon knows.", req.Harness),
			"Use "+d.harnessNames()+"."), false
	}
	vctx, cancel := context.WithTimeout(ctx, harnessCallTimeout)
	err := ad.Validate(vctx, req.Session)
	cancel()
	if r, failed := validationResponse(req.Key(), err); failed {
		return r, false
	}
	if ad.WaitsForIdle() && d.session(req.Key(), false) == nil {
		return sessionUnknown(req.Key()), false
	}
	if d.cfg.Seats == nil {
		return errorResponse("internal", "This delivery daemon can't join boards.", "Run aboard down, then the command again."), false
	}
	return Response{}, true
}

// sessionAgents returns the agents bound to the session.
func (d *Daemon) sessionAgents(ctx context.Context, req Request) ([]AgentRef, *WireError) {
	r := d.call(ctx, Request{V: ProtocolVersion, Op: OpAgents, Harness: req.Harness, Session: req.Session})
	if r.Error != nil && r.Error.Code != "session_unknown" {
		return nil, r.Error
	}
	return r.Agents, nil
}

// seatsError turns what Seats returned into the answer for the command.
func seatsError(server string, err error) Response {
	var refusal *WireError
	switch {
	case errors.As(err, &refusal):
		return Response{V: ProtocolVersion, Error: refusal}
	case errors.Is(err, ErrLoginMissing):
		return errorResponse("login_required", "This machine has no key for "+server+".",
			"Your person runs aboard connect with an invite link for "+server+", or aboard login, on this machine.")
	case errors.Is(err, ErrServerOutdated):
		return errorResponse("server_outdated", "The server at "+server+" can't list or join boards for a session yet.",
			"Ask its admin to upgrade it, or join with a join line: a person runs aboard invite --board NAME in a terminal.")
	default:
		return errorResponse("server_unreachable", "Couldn't reach "+server+": "+err.Error(),
			"Check the server and the network, then run the command again.")
	}
}

// serveBoards answers OpBoards: the boards the session's person can see on a server,
// with the session's seat on each.
func (d *Daemon) serveBoards(ctx context.Context, req Request) Response {
	if r, ok := d.seatsCheck(ctx, req); !ok {
		return r
	}
	agents, werr := d.sessionAgents(ctx, req)
	if werr != nil {
		return Response{V: ProtocolVersion, Error: werr}
	}
	server := req.Server
	if server == "" && len(agents) > 0 {
		server = agents[0].Server
	}
	if server == "" {
		return errorResponse("invalid_request", "A boards request needs a server.", "Send the server the session's person is connected to.")
	}
	boards, err := d.cfg.Seats.Boards(ctx, server)
	if err != nil {
		return seatsError(server, err)
	}
	seats := map[string]SeatRef{}
	for _, a := range agents {
		if a.Server != server {
			continue
		}
		seat := SeatRef{Server: a.Server, Board: a.Board, Name: a.Name}
		seat.MemberID, _ = d.cfg.Seats.SeatID(a)
		seats[a.Board] = seat
	}
	out := Response{V: ProtocolVersion, Server: server, Boards: []json.RawMessage{}}
	for _, b := range boards {
		raw := b.Board
		if seat, ok := seats[b.Name]; ok {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return errorResponse("internal", "The server's list of boards isn't JSON: "+err.Error(), "Look at the daemon log.")
			}
			m["seat"] = seat
			if raw, err = json.Marshal(m); err != nil {
				return errorResponse("internal", "Couldn't encode the list of boards: "+err.Error(), "Look at the daemon log.")
			}
		}
		out.Boards = append(out.Boards, raw)
	}
	return out
}

// serveJoin answers OpJoin: the server gives the session a seat on the board through
// the delegation, the daemon saves its token and then binds it as OpBind does. Until
// several seats per session are switched on, binding keeps the one-seat rule: a new
// seat ends the session's other binding, answered as previous.
func (d *Daemon) serveJoin(ctx context.Context, req Request) Response {
	if r, ok := d.seatsCheck(ctx, req); !ok {
		return r
	}
	if req.Agent == nil || req.Agent.Server == "" || req.Agent.Board == "" {
		return errorResponse("invalid_request", "A join request needs the board and its server.", "Send agent.server and agent.board.")
	}
	server, board := req.Agent.Server, req.Agent.Board
	// One join at a time for the session: choosing the server, the server's answer and
	// the binding happen under this turn, so two first joins can't both pass the
	// one-server rule, and a reused seat's earlier token, which stops the moment the
	// server answers, is never rotated by two joins at once. The session's seats are
	// read only once the turn is held.
	release, err := d.joinTurn(ctx, req.Key().String())
	if err != nil {
		return errorResponse("daemon_not_running", "The delivery daemon is stopping.", "Run the command again; it starts the daemon.")
	}
	defer release()
	agents, werr := d.sessionAgents(ctx, req)
	if werr != nil {
		return Response{V: ProtocolVersion, Error: werr}
	}
	if h := d.cfg.joinHooks; h != nil && h.seatsRead != nil {
		h.seatsRead()
	}
	for _, a := range agents {
		if a.Server != server {
			r := errorResponse("session_on_another_server",
				"This session's seats are on "+a.Server+", and a session's seats are all on one server.",
				"Use a session for "+server+": start one and run the join there.")
			r.Error.Details = map[string]any{"server": server, "session_server": a.Server}
			return r
		}
	}
	grant, err := d.cfg.Seats.Join(ctx, server, SeatRequest{
		Board: board, Role: req.Role, Name: req.Agent.Name, Harness: req.Harness, Session: req.Key().String(),
	})
	if err != nil {
		d.log.Warn("join through the delegation", "server", server, "board", board, "session", req.Key().String(), "error", err)
		return seatsError(server, err)
	}
	// The token is saved before the seat is bound or success answered, so the token a
	// session holds is always the newest one the server issued.
	if err := d.cfg.Seats.Save(ctx, grant.Seat, grant.Token); err != nil {
		d.log.Warn("save a joined seat's token", "server", server, "board", board, "error", err)
		return errorResponse("internal", "Couldn't save the seat's token: "+err.Error(),
			"Check that this machine's aboard config folder is writable, then run the join again.")
	}
	agent := grantAgent(grant)
	bound := d.call(ctx, Request{V: ProtocolVersion, Op: OpBind, Harness: req.Harness, Session: req.Session, Agent: &agent, Process: req.Process})
	if bound.Error != nil {
		return bound
	}
	mode := grant.Mode
	if mode == "" {
		mode = d.mode(agent.byName())
	}
	seat := grant.Seat
	d.log.Info("session joined a board through the delegation", "session", req.Key().String(), "board", seat.Board,
		"agent", seat.Name, "member", seat.MemberID, "reused", grant.Reused)
	return Response{
		V: ProtocolVersion, Joined: &seat, Reused: grant.Reused, Board: grant.Board, Member: grant.Member,
		Mode: mode, Previous: bound.Previous,
	}
}

// grantAgent is the seat a join is bound as: by its member id, with its board and name
// for display.
func grantAgent(g SeatGrant) AgentRef {
	return AgentRef{Server: g.Seat.Server, Board: g.Seat.Board, Name: g.Seat.Name, MemberID: g.Seat.MemberID}
}

// joinTurn waits for the turn to join for key, and returns what ends it.
func (d *Daemon) joinTurn(ctx context.Context, key string) (func(), error) {
	d.mu.Lock()
	if d.joining == nil {
		d.joining = map[string]chan struct{}{}
	}
	turn, ok := d.joining[key]
	if !ok {
		turn = make(chan struct{}, 1)
		d.joining[key] = turn
	}
	d.mu.Unlock()
	select {
	case turn <- struct{}{}:
		return func() { <-turn }, nil
	default:
	}
	if h := d.cfg.joinHooks; h != nil && h.waiting != nil {
		h.waiting()
	}
	select {
	case turn <- struct{}{}:
		return func() { <-turn }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// joinHooks let tests hold a join at its points of contention; nil otherwise.
type joinHooks struct {
	seatsRead func() // the join read the session's seats, before checking its server
	waiting   func() // the join found the session's turn taken and is about to wait
}
