package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type setupHello struct {
	BoardID   string `json:"board_id"`
	MemberID  string `json:"member_id"`
	MessageID string `json:"message_id"`
	Seq       int    `json:"seq"`
}

func (a *app) continueSetupGreeting(ctx context.Context, out *setupOutput, id string, pending *setupPending) error {
	out.Steps[5].Message = "Waiting for a reply from the inviting person's agents."
	key, ok := a.sessionKey()
	if !ok {
		return nil
	}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpPairing, Harness: key.Harness, Session: key.ID, Server: out.Server.URL, PairingAction: "get", PairingID: id})
	if err != nil || resp.Server != out.Server.URL || resp.Pairing == nil || resp.Pairing.ID != id || resp.Pairing.BoardID == "" || resp.Pairing.InviterID == "" {
		return nil
	}
	if pending != nil && (pending.Receipt == nil || !slices.Contains(pending.Receipt.Boards, resp.Pairing.BoardID)) {
		return nil
	}
	inviter := "the inviting person"
	var display api.OnboardingDisplay
	if json.Unmarshal(resp.Pairing.Display, &display) == nil && display.PersonHandle != nil {
		inviter = "@" + *display.PersonHandle
	}
	command := out.continueCommand
	if command == "" {
		command = "aboard setup " + commandWord(id) + " --server " + commandWord(out.Server.URL)
	}
	out.Steps[5].Message = "Waiting for a reply from " + inviter + "'s agents."
	if out.Steps[3].State != "complete" {
		return nil
	}
	out.Next = &api.NextStep{Command: command, Resume: "The inviting person's agents will greet you when they next run. Continue Aboard setup to check for a reply."}
	agents, err := a.sessionAgents(ctx, key)
	if err != nil {
		return err
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.Server != out.Server.URL || agent.MemberID == "" {
			continue
		}
		cred, ok := creds.forSeat(agent)
		if !ok {
			continue
		}
		c, err := a.client(ctx, out.Server, cred.Token, requestTimeout)
		if err != nil {
			return err
		}
		me, err := c.api.GetMeWithResponse(ctx)
		if err != nil {
			return c.unreachable(err)
		}
		if me.JSON200 == nil || me.JSON200.Id != agent.MemberID || me.JSON200.Board == nil {
			continue
		}
		board, err := c.api.GetBoardWithResponse(ctx, *me.JSON200.Board)
		if err != nil {
			return c.unreachable(err)
		}
		if board.JSON200 == nil || board.JSON200.Id != resp.Pairing.BoardID || !board.JSON200.OnBoard || (board.JSON200.Lifecycle != nil && *board.JSON200.Lifecycle != api.BoardLifecycleActive) {
			continue
		}
		out.Steps[4].State = "complete"
		out.Steps[4].Message = "This exact session joined the invited boards; existing seats were reused."
		hello, err := a.setupGreeting(ctx, c, agent, board.JSON200.Id, id, pending, out.Server)
		if err != nil {
			return err
		}
		rd := a.startInboxRead(ctx, agent, true)
		received := len(rd.received) > 0 && (a.env.Getenv("ABOARD_BOOT") == "" || rd.boot == a.env.Getenv("ABOARD_BOOT"))
		rd.done()
		answered, err := setupGreetingAnswered(ctx, c, hello, resp.Pairing.InviterID)
		if err != nil {
			return err
		}
		if received || answered {
			out.Steps[5].State = "complete"
			out.Steps[5].Message = "Messages get through on the invited board."
			out.State = "complete"
			out.Next = nil
		}
		return nil
	}
	return nil
}

func (a *app) setupGreeting(ctx context.Context, c *client, agent delivery.AgentRef, boardID, requestID string, pending *setupPending, srv serverRef) (*setupHello, error) {
	if pending != nil && pending.Hello != nil && pending.Hello.BoardID == boardID && pending.Hello.MemberID == agent.MemberID {
		return pending.Hello, nil
	}
	sum := sha256.Sum256([]byte(srv.URL + "\x00" + requestID + "\x00" + agent.MemberID))
	idempotency := api.IdempotencyKey("setup-hello-" + hex.EncodeToString(sum[:]))
	expects := true
	posted, err := c.api.PostMessageWithResponse(ctx, agent.Board, &api.PostMessageParams{IdempotencyKey: &idempotency}, api.PostMessageRequest{Body: "Hello, I joined using your invitation. Please reply so I can check that messages get through.", ExpectsReply: &expects})
	if err != nil {
		return nil, c.unreachable(err)
	}
	if posted.JSON201 == nil {
		return nil, apiError(posted.StatusCode(), posted.Body)
	}
	hello := &setupHello{BoardID: boardID, MemberID: agent.MemberID, MessageID: posted.JSON201.Id, Seq: posted.JSON201.Seq}
	if pending != nil {
		path, err := a.setupPendingPath(srv, pending.Invite)
		if err != nil {
			return nil, err
		}
		lock, err := lockSetup(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = lock.Close() }()
		latest, err := readSetupPending(path)
		if err != nil {
			return nil, err
		}
		if latest == nil || latest.Server != pending.Server || latest.Token != pending.Token {
			return nil, newError("setup_state_invalid", "The saved setup changed before its greeting was recorded.", "Continue the original saved setup.")
		}
		latest.Hello = hello
		if err := saveSetupPending(path, latest); err != nil {
			return nil, err
		}
		pending.Hello = hello
	}
	return hello, nil
}

func setupGreetingAnswered(ctx context.Context, c *client, hello *setupHello, inviterID string) (bool, error) {
	thread, err := c.thread(ctx, hello.MessageID)
	if err != nil {
		return false, err
	}
	if thread.Root == nil || thread.Root.Id != hello.MessageID || thread.Root.Seq != hello.Seq {
		return false, nil
	}
	members, err := c.api.ListMembersWithResponse(ctx, thread.Root.Board, nil)
	if err != nil {
		return false, c.unreachable(err)
	}
	if members.JSON200 == nil {
		return false, apiError(members.StatusCode(), members.Body)
	}
	eligible := map[string]bool{}
	for _, member := range members.JSON200.Members {
		if member.Kind == api.MemberKindAgent && member.Status == api.MemberStatusActive && member.OwnerId != nil && *member.OwnerId == inviterID {
			eligible[member.Id] = true
		}
	}
	for _, reply := range thread.Replies {
		if reply.ReplyTo == nil || strings.TrimSpace(*reply.ReplyTo) != hello.MessageID {
			continue
		}
		page, err := c.eventsPage(ctx, thread.Root.Board, reply.Seq-1)
		if err != nil {
			return false, err
		}
		for _, event := range page.Events {
			if event.Seq == int64(reply.Seq) && event.BoardID == hello.BoardID && event.Actor.Kind == "agent" && event.Actor.MemberID != nil && eligible[*event.Actor.MemberID] {
				return true, nil
			}
		}
	}
	return false, nil
}
