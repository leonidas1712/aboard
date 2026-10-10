package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PairingRequest is the nonsecret public request view carried on the control socket.
type PairingRequest struct {
	Display           json.RawMessage  `json:"display,omitempty"`
	ID                string           `json:"id"`
	ServerID          string           `json:"server_id"`
	BoardID           string           `json:"board_id"`
	InviterID         string           `json:"inviter_id"`
	InitiatingAgentID string           `json:"initiating_agent_id"`
	RecipientID       string           `json:"recipient_id,omitempty"`
	InviteID          string           `json:"invite_id,omitempty"`
	Work              string           `json:"work"`
	State             string           `json:"state"`
	Generation        int              `json:"generation"`
	Initiator         *PairingEndpoint `json:"initiator,omitempty"`
	Recipient         *PairingEndpoint `json:"recipient,omitempty"`
	Awaiting          string           `json:"awaiting,omitempty"`
	CreatedAt         string           `json:"created_at"`
	ExpiresAt         string           `json:"expires_at"`
	Next              json.RawMessage  `json:"next,omitempty"`
}

// PairingEndpoint identifies one selected permanent seat and exact runtime binding.
type PairingEndpoint struct {
	PersonID       string `json:"person_id"`
	AgentID        string `json:"agent_id"`
	Generation     int    `json:"generation"`
	SessionBinding string `json:"session_binding"`
}

// PairingCreate is the public, nonsecret proposal payload.
type PairingCreate struct {
	BoardID           string `json:"board_id"`
	RecipientID       string `json:"recipient_id"`
	InitiatingAgentID string `json:"initiating_agent_id"`
	Work              string `json:"work"`
}

// PairingRuntime holds issuer-bound parent and endpoint authority behind the socket.
// Select saves the endpoint secret before minting; no secret is returned here.
type PairingRuntime interface {
	List(context.Context) ([]PairingRequest, error)
	PersonID(context.Context) (string, error)
	Get(context.Context, string) (PairingRequest, error)
	Create(context.Context, PairingCreate, AgentRef, string) (PairingRequest, error)
	Close(context.Context, string, string, string) (PairingRequest, error)
	Select(context.Context, PairingRequest, string, AgentRef, string, bool, string) (PairingRequest, error)
	Progress(context.Context, PairingRequest, AgentRef, string, []Delivery) (PairingRequest, error)
}

func (d *Daemon) servePairing(ctx context.Context, req Request) Response {
	if req.Harness == "" || req.Session == "" {
		return errorResponse("agent_session_required", "Pairing needs the exact live harness session.", "Run aboard pairing inside the session that should participate.")
	}
	if _, ok := d.adapters[req.Harness]; !ok {
		return errorResponse("agent_session_required", "This harness session is not trusted.", "Use a supported live harness session.")
	}
	if r, ok := d.seatsCheck(ctx, req); !ok {
		return r
	}
	if req.Server == "" || d.cfg.PairingFor == nil {
		return errorResponse("invalid_request", "Pairing needs a server and a trusted runtime.", "Send the issuer server and restart an up-to-date daemon.")
	}
	agents, werr := d.sessionAgents(ctx, req)
	if werr != nil {
		return Response{V: 1, Error: werr}
	}
	issuerAgents := agents[:0]
	for _, agent := range agents {
		if agent.Server == req.Server {
			issuerAgents = append(issuerAgents, agent)
		}
	}
	agents = issuerAgents

	runtime := d.cfg.PairingFor(req.Server)
	if runtime == nil {
		return errorResponse("internal", "Pairing is unavailable in this daemon.", "Restart an up-to-date daemon.")
	}
	rows, err := runtime.List(ctx)
	if err != nil {
		return seatsError(req.Server, err)
	}
	if req.PairingAction == "list" || req.PairingAction == "" {
		return Response{V: 1, Server: req.Server, Pairings: rows}
	}
	if req.PairingAction != "get" && (req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128) {
		return errorResponse("invalid_request", "Pairing writes need a stable idempotency key.", "Send an idempotency_key of 1 to 128 characters.")
	}
	if req.PairingAction == "request" {
		agents, werr := d.sessionAgents(ctx, req)
		if werr != nil {
			return Response{V: 1, Error: werr}
		}
		var selected AgentRef
		for _, a := range agents {
			if a.Server == req.Server && a.MemberID == req.InitiatingAgentID {
				selected = a
			}
		}
		if selected.MemberID == "" {
			return errorResponse("agent_not_selected", "The initiating seat is not in this exact session.", "Select one of this session's seats.")
		}
		got, err := runtime.Create(ctx, PairingCreate{req.BoardID, req.RecipientID, selected.MemberID, req.Work}, selected, req.IdempotencyKey)
		if err != nil {
			return seatsError(req.Server, err)
		}
		if got.State == "awaiting_endpoint" {
			return Response{V: 1, Server: req.Server, PairingBoard: selected.Board, Pairing: &got}
		}
		binding, err := d.pairingBinding(ctx, req.Key(), selected)
		if err != nil {
			return seatsError(req.Server, err)
		}
		got, err = runtime.Select(ctx, got, "initiator", selected, binding, false, req.IdempotencyKey)
		if err != nil {
			return seatsError(req.Server, err)
		}
		d.watchPairing(ctx, req.Key(), runtime, got, selected, binding)
		return Response{V: 1, Server: req.Server, PairingBoard: selected.Board, Pairing: &got}
	}
	var current PairingRequest
	for _, row := range rows {
		if row.ID == req.PairingID {
			current = row
		}
	}
	if req.PairingID == "" && req.PairingAction == "accept" {
		ownPerson, err := runtime.PersonID(ctx)
		if err != nil {
			return seatsError(req.Server, err)
		}
		pending := []PairingRequest{}
		for _, row := range rows {
			if row.RecipientID == ownPerson && row.State != "ready" && row.State != "declined" && row.State != "cancelled" && row.State != "expired" {
				pending = append(pending, row)
			}
		}
		if len(pending) != 1 {
			r := errorResponse("pairing_ambiguous", "Choose one visible pairing request.", "Run aboard pairing list, then accept its id with --here.")
			ids := []string{}
			for _, p := range pending {
				ids = append(ids, p.ID)
			}
			r.Error.Details = map[string]any{"requests": ids}
			return r
		}
		current = pending[0]
	}
	if current.ID == "" {
		return errorResponse("pairing_not_found", "That pairing request is not available.", "Run aboard pairing list.")
	}
	if req.PairingAction == "decline" || req.PairingAction == "cancel" {
		got, err := runtime.Close(ctx, current.ID, req.PairingAction, req.IdempotencyKey)
		if err != nil {
			return seatsError(req.Server, err)
		}
		return Response{V: 1, Server: req.Server, PairingBoard: d.pairingBoard(ctx, req.Server, got.BoardID), Pairing: &got}
	}
	if req.PairingAction != "accept" && req.PairingAction != "select" && req.PairingAction != "get" {
		return errorResponse("invalid_request", "Unknown pairing action.", "Use list, request, get, select, accept, decline or cancel.")
	}
	if req.PairingAction == "get" {
		return Response{V: 1, Server: req.Server, PairingBoard: d.pairingBoard(ctx, req.Server, current.BoardID), Pairing: &current}
	}
	ownPerson, err := runtime.PersonID(ctx)
	if err != nil {
		return seatsError(req.Server, err)
	}
	expectedPerson := current.RecipientID
	if req.PairingAction == "select" {
		expectedPerson = current.InviterID
	}
	if expectedPerson == "" || ownPerson != expectedPerson {
		return errorResponse("forbidden", "This person cannot select that pairing side.", "Ask the selected side's person to use their own session.")
	}
	if current.State == "ready" && !req.Replace {
		return d.readyPairing(ctx, req, runtime, current)
	}
	if current.State == "ready" || current.State == "declined" || current.State == "cancelled" || current.State == "expired" {
		return errorResponse("pairing_closed", "This pairing request has ended.", "Create a new pairing request.")
	}
	boards, err := d.cfg.Seats.Boards(ctx, req.Server, "active")
	if err != nil {
		return seatsError(req.Server, err)
	}
	var board string
	for _, row := range boards.Boards {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row.Board, &identity) == nil && identity.ID == current.BoardID {
			board = row.Name
		}
	}
	if board == "" {
		return errorResponse("board_not_found", "The pairing board is not available.", "Ask the inviter to check your board membership.")
	}
	agents, werr = d.sessionAgents(ctx, req)
	if werr != nil {
		return Response{V: 1, Error: werr}
	}
	var selected AgentRef
	for _, a := range agents {
		if a.Server == req.Server && a.Board == board && (req.PairingAction != "select" || req.Replace || a.MemberID == current.InitiatingAgentID) {
			selected = a
		}
	}
	if selected.MemberID == "" && req.PairingAction == "select" && !req.Replace {
		return errorResponse("agent_not_selected", "The initiating seat is not bound to this exact session.", "Resume the initiating agent here, then select the endpoint.")
	}
	if selected.MemberID == "" {
		joined := d.serveJoin(ctx, Request{V: 1, Harness: req.Harness, Session: req.Session, Server: req.Server, Agent: &AgentRef{Server: req.Server, Board: board}, AgentName: req.AgentName})
		if joined.Error != nil {
			return joined
		}
		if joined.Joined == nil {
			return errorResponse("internal", "The join returned no selected seat.", "Run the pairing command again.")
		}
		selected = AgentRef{Server: joined.Joined.Server, Board: joined.Joined.Board, Name: joined.Joined.Name, MemberID: joined.Joined.MemberID}
	}
	binding, err := d.pairingBinding(ctx, req.Key(), selected)
	if err != nil {
		return seatsError(req.Server, err)
	}
	side := "recipient"
	if req.PairingAction == "select" {
		side = "initiator"
	}
	got, err := runtime.Select(ctx, current, side, selected, binding, req.Replace, req.IdempotencyKey)
	if err != nil {
		return seatsError(req.Server, err)
	}
	d.watchPairing(ctx, req.Key(), runtime, got, selected, binding)
	return Response{V: 1, Server: req.Server, PairingBoard: board, Pairing: &got}
}

func (d *Daemon) readyPairing(ctx context.Context, req Request, runtime PairingRuntime, listed PairingRequest) Response {
	current, err := runtime.Get(ctx, listed.ID)
	if err != nil {
		return seatsError(req.Server, err)
	}
	if current.ID != listed.ID || current.State != "ready" || current.Generation <= 0 || current.Generation != listed.Generation {
		return errorResponse("pairing_changed", "The pairing proof changed.", "Read the request before selecting another endpoint.")
	}
	endpoint := current.Recipient
	if req.PairingAction == "select" {
		endpoint = current.Initiator
	}
	ownPerson, err := runtime.PersonID(ctx)
	if err != nil {
		return seatsError(req.Server, err)
	}
	if endpoint == nil || endpoint.PersonID != ownPerson || endpoint.AgentID == "" || endpoint.Generation != current.Generation || endpoint.SessionBinding == "" {
		return errorResponse("pairing_changed", "The recorded endpoint does not match this pairing generation.", "Use the selected endpoint's original session.")
	}
	boards, err := d.cfg.Seats.Boards(ctx, req.Server, "active")
	if err != nil {
		return seatsError(req.Server, err)
	}
	var board string
	for _, row := range boards.Boards {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row.Board, &identity) == nil && identity.ID == current.BoardID {
			board = row.Name
		}
	}
	if board == "" {
		return errorResponse("board_not_found", "The pairing board is not available.", "Ask the inviter to check your board membership.")
	}
	agents, werr := d.sessionAgents(ctx, req)
	if werr != nil {
		return Response{V: ProtocolVersion, Error: werr}
	}
	for _, agent := range agents {
		if agent.Server != req.Server || agent.Board != board || agent.MemberID != endpoint.AgentID {
			continue
		}
		verified, err := d.resolveAgent(ctx, agent)
		if err != nil {
			return seatsError(req.Server, err)
		}
		binding, err := d.pairingBinding(ctx, req.Key(), verified)
		if err != nil {
			return seatsError(req.Server, err)
		}
		if binding != endpoint.SessionBinding {
			break
		}
		return Response{V: ProtocolVersion, Server: req.Server, PairingBoard: board, Pairing: &current}
	}
	return errorResponse("pairing_changed", "The ready endpoint is not bound to this exact current session.", "Use the original session; ready pairings cannot replace their endpoint.")
}

func (d *Daemon) pairingBinding(ctx context.Context, key SessionKey, agent AgentRef) (string, error) {
	sessions, err := d.cfg.Journal.Sessions(ctx)
	if err != nil {
		return "", err
	}
	var boot string
	for _, s := range sessions {
		if s.Key == key && s.Open {
			boot = s.Boot
		}
	}
	if boot == "" {
		return "", &WireError{Code: "agent_session_required", Message: "The selected session is not open.", Hint: "Resume the exact harness session."}
	}
	bindings, err := d.cfg.Journal.Bindings(ctx)
	if err != nil {
		return "", err
	}
	for _, b := range bindings {
		if b.Session == key && b.Agent.Key() == agent.Key() {
			digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%s", agent.Server, agent.MemberID, key.String(), boot, b.Generation, d.pairingBoot)))
			return "sha256:" + hex.EncodeToString(digest[:]), nil
		}
	}
	return "", &WireError{Code: "pairing_changed", Message: "The selected seat changed sessions.", Hint: "Select the endpoint again explicitly."}
}

// watchPairing owns a bounded verification loop; the daemon cancels and joins it.
// Only already-confirmed handoffs for the still-selected binding enter Progress.
func (d *Daemon) watchPairing(ctx context.Context, key SessionKey, runtime PairingRuntime, current PairingRequest, agent AgentRef, binding string) {
	identity := agent.Server + "\x00" + current.ID + "\x00" + key.String() + "\x00" + binding
	d.mu.Lock()
	if d.pairingWatching == nil {
		d.pairingWatching = map[string]bool{}
	}
	if d.pairingWatching[identity] {
		d.mu.Unlock()
		return
	}
	d.pairingWatching[identity] = true
	d.mu.Unlock()
	d.g.Go(func() error {
		defer func() { d.mu.Lock(); delete(d.pairingWatching, identity); d.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			fresh, err := runtime.Get(ctx, current.ID)
			if err == nil {
				nowBinding, e := d.pairingBinding(ctx, key, agent)
				if e != nil || nowBinding != binding {
					return nil
				}
				rows, e := d.confirmedPairingDeliveries(ctx, key, agent)
				if e != nil {
					return nil
				}
				fresh, err = runtime.Progress(ctx, fresh, agent, binding, rows)
				if err == nil && (fresh.State == "ready" || fresh.State == "declined" || fresh.State == "cancelled" || fresh.State == "expired") {
					return nil
				}
			}
			var wire *WireError
			if errors.As(err, &wire) {
				return nil
			}
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
		}
	})
}

func (d *Daemon) confirmedPairingDeliveries(ctx context.Context, key SessionKey, agent AgentRef) ([]Delivery, error) {
	return confirmedPairingDeliveries(ctx, d.cfg.Journal, key, agent)
}

func confirmedPairingDeliveries(ctx context.Context, j Journal, key SessionKey, agent AgentRef) ([]Delivery, error) {
	sessions, err := j.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var boot string
	for _, s := range sessions {
		if s.Key == key && s.Open {
			boot = s.Boot
		}
	}
	if boot == "" {
		return nil, nil
	}
	bindings, err := j.Bindings(ctx)
	if err != nil {
		return nil, err
	}
	var generation uint64
	for _, b := range bindings {
		if b.Session == key && b.Agent.Key() == agent.Key() {
			generation = b.Generation
		}
	}
	manifests, err := j.Handoffs(ctx)
	if err != nil {
		return nil, err
	}
	eligible := map[string]map[int]bool{}
	for _, m := range manifests {
		if m.Session != key || m.Boot != boot {
			continue
		}
		for _, part := range m.Parts {
			if part.Agent.Key() == agent.Key() && part.Generation == generation {
				if eligible[m.ID] == nil {
					eligible[m.ID] = map[int]bool{}
				}
				for _, seq := range part.Seqs {
					eligible[m.ID][seq] = true
				}
			}
		}
	}
	rows, err := j.Deliveries(ctx, StateConfirmed, StateDone)
	if err != nil {
		return nil, err
	}
	out := []Delivery{}
	for _, row := range rows {
		if row.Session != key || row.Boot != boot || row.Agent.Key() != agent.Key() || row.HandoffID == "" || row.ConfirmedAt.IsZero() {
			continue
		}
		selected := row
		selected.State = StateConfirmed
		selected.Seqs = nil
		for _, seq := range row.Seqs {
			if eligible[row.HandoffID][seq] {
				selected.Seqs = append(selected.Seqs, seq)
			}
		}
		if len(selected.Seqs) > 0 {
			out = append(out, selected)
		}
	}
	return out, nil
}

func (d *Daemon) pairingBoard(ctx context.Context, server, id string) string {
	boards, err := d.cfg.Seats.Boards(ctx, server, "all")
	if err != nil {
		return ""
	}
	for _, row := range boards.Boards {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row.Board, &identity) == nil && identity.ID == id {
			return row.Name
		}
	}
	return ""
}
