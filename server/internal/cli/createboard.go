package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// createSessionBoard asks the daemon to create and bind a seat without exposing its
// machine delegation or the person's key to this command.
func (a *app) createSessionBoard(ctx context.Context, key delivery.SessionKey, srv serverRef, options delivery.BoardCreateOptions, role, name string) (*api.JoinResult, delivery.Response, error) {
	operation, err := idempotencyKey(a.env.Rand)
	if err != nil {
		return nil, delivery.Response{}, err
	}
	req := delivery.Request{Op: delivery.OpCreateBoard, Harness: key.Harness, Session: key.ID, Server: srv.URL, Create: &options, Role: role, AgentName: name, IdempotencyKey: operation}
	resp, err := a.callDaemon(ctx, req)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && (e.Code == "server_unreachable" || e.Code == "internal" || e.Code == "daemon_not_running") {
			if e.Details == nil {
				e.Details = map[string]any{}
			}
			e.Details["idempotency_key"] = operation
			e.Hint += " Check aboard boards and deliberately join the created board before starting another creation; a restarted daemon cannot recover this operation's original delegation scope."
		}
		return nil, resp, err
	}
	if resp.Joined == nil {
		return nil, resp, newError("internal", "The daemon answered creation without a seat.", "Check aboard boards before creating another board.")
	}
	var board api.Board
	var member api.Member
	if err := json.Unmarshal(resp.Board, &board); err != nil {
		return nil, resp, fmt.Errorf("read created board: %w", err)
	}
	if err := json.Unmarshal(resp.Member, &member); err != nil {
		return nil, resp, fmt.Errorf("read created seat: %w", err)
	}
	return &api.JoinResult{Board: board, Agent: member}, resp, nil
}

func creationNeedsSession() error {
	return newError("agent_session_required", "An agent can create a board only from a real harness session.", "Run this command inside the session that should get the new board's seat.")
}

func (a *app) runSessionBoardNew(ctx context.Context, key delivery.SessionKey, name, title string, private bool, serverFlag string) error {
	if _, _, err := a.checkSession(ctx); err != nil {
		return err
	}
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return err
	}
	sessionServer, err := sessionIssuer(agents, serverFlag)
	if err != nil {
		return err
	}
	var srv serverRef
	if sessionServer == "" {
		srv, _, err = a.bootstrapServer(ctx, serverFlag)
	} else {
		srv, err = a.boardServer(ctx, serverFlag, sessionServer)
	}
	if err != nil {
		return err
	}
	visibility := "open"
	if private {
		visibility = "private"
	}
	joined, resp, err := a.createSessionBoard(ctx, key, srv, delivery.BoardCreateOptions{Name: name, Title: title, Template: "general", Visibility: visibility}, "member", "")
	if err != nil {
		return err
	}
	linked := false
	var stays *staysLinked
	use := useFor(joined.Agent.Name)
	use.BoundSession = optional(key.String())
	mode := a.deliveryFor(ctx, delivery.AgentRef{Server: srv.URL, Board: joined.Board.Name, Name: joined.Agent.Name, MemberID: joined.Agent.Id}, string(resp.Mode))
	notice := noticeFor(joined.Board.Policy)
	text := fmt.Sprintf("Created board %s on %s and joined as %s.\n", joined.Board.Name, srv.URL, agentText(joined.Agent)) + mode.line()
	if notice != nil {
		text += notice.Message + "\n"
	}
	text += a.seatBoardReminder(ctx, key, joined.Board.Name)
	joinCommand := "aboard join --server " + commandWord(srv.URL) + " --board " + commandWord(joined.Board.Name)
	a.emit(struct {
		Server      serverRef     `json:"server"`
		Board       api.Board     `json:"board"`
		Linked      bool          `json:"linked"`
		Stays       *staysLinked  `json:"stays_linked"`
		Notice      *policyNotice `json:"policy_notice"`
		JoinCommand string        `json:"join_command"`
		Agent       api.Member    `json:"agent"`
		Use         agentUse      `json:"use"`
		seatDelivery
	}{srv, joined.Board, linked, stays, notice, joinCommand, joined.Agent, use, mode}, text)
	return nil
}
