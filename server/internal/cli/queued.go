package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

func unknownQueue() *Error {
	return &Error{Code: "queue_unknown", Message: "This session's queue could not be verified.", Hint: "Use the current session with a running delivery daemon; nothing was acknowledged."}
}

// observeQueued never starts a daemon or sends a prompt, claim or receipt.
func (a *app) observeQueued(ctx context.Context, servers ...string) (delivery.Response, error) {
	issuer, err := a.agentIssuer()
	if err != nil {
		return delivery.Response{}, err
	}
	if len(servers) > 0 && servers[0] != "" {
		issuer = servers[0]
	}
	key, ok := a.sessionKey()
	if !ok {
		return delivery.Response{}, unknownQueue()
	}
	p, err := a.paths()
	if err != nil {
		return delivery.Response{}, err
	}
	dctx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	conn, err := control.Dial(dctx, p.socket())
	if err != nil {
		return delivery.Response{}, unknownQueue()
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(daemonCallTimeout))
	req := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpQueued, Server: issuer, Harness: key.Harness, Session: key.ID, Boot: a.env.Getenv("ABOARD_BOOT")}
	var resp delivery.Response
	if delivery.WriteFrame(conn, req) != nil || delivery.ReadFrame(bufio.NewReader(conn), &resp) != nil {
		return resp, unknownQueue()
	}
	if resp.Error != nil {
		return resp, &Error{Code: resp.Error.Code, Message: resp.Error.Message, Hint: resp.Error.Hint}
	}
	if resp.Queued == nil {
		return resp, unknownQueue()
	}
	return normalizeQueuedObservation(resp)
}

func queuedText(q *delivery.QueuedMessages) string {
	if q == nil || q.Count == 0 {
		return ""
	}
	boards := map[string]bool{}
	issuers := map[string]bool{}
	for _, m := range q.Messages {
		issuers[m.Server] = true
		boards[m.Server+"\x00"+m.BoardID] = true
	}
	names := make([]string, 0, len(q.Messages))
	for _, m := range q.Messages {
		label := fmt.Sprintf("#%d from %s", m.Seq, m.From)
		if len(boards) > 1 {
			board := m.Board
			if board == "" {
				board = m.BoardID
			}
			label = board + " " + label
		}
		if len(issuers) > 1 {
			label = m.Server + " / " + label
		}
		names = append(names, label)
	}
	return fmt.Sprintf("%d queued, arriving at the end of this turn: %s\n", q.Count, strings.Join(names, ", "))
}

func (a *app) queuedPreview(ctx context.Context, observation delivery.Response, creds credentials, board, name string, limit int) (inboxSeatsOutput, error) {
	out := inboxSeatsOutput{Seats: []inboxSeat{}, Queued: &delivery.QueuedMessages{Messages: []delivery.QueuedMessage{}}}
	issuer, err := a.agentIssuer()
	if err != nil {
		return out, err
	}
	observation, err = normalizeQueuedObservation(observation)
	if err != nil {
		return out, err
	}
	selected := false
	for _, ref := range observation.Agents {
		if issuer != "" && ref.Server != issuer {
			continue
		}
		if board != "" && ref.Board != board {
			continue
		}
		if name != "" && ref.Name != strings.TrimPrefix(name, "@") {
			continue
		}
		selected = true
		cred, ok := seatCredential(creds, ref.Server, ref.MemberID)
		if !ok {
			return out, unknownQueue()
		}
		c, err := a.client(ctx, a.serverRefFor(ref.Server), cred.Token, requestTimeout)
		if err != nil {
			return out, err
		}
		rctx, cancel := context.WithTimeout(ctx, requestTimeout)
		in, err := c.api.GetInboxWithResponse(rctx, nil)
		cancel()
		if err != nil {
			return out, c.unreachable(err)
		}
		if in.JSON200 == nil {
			return out, apiError(in.StatusCode(), in.Body)
		}
		if in.JSON200.BoardId == nil || in.JSON200.MemberId == nil || *in.JSON200.MemberId != ref.MemberID {
			return out, unknownQueue()
		}
		seat := inboxSeat{Server: ref.Server, Board: ref.Board, Agent: ref.Name, MemberID: ref.MemberID, Messages: []cliMessage{}, Wrapped: []string{}}
		for _, q := range observation.Queued.Messages {
			if q.Server != ref.Server || q.MemberID != ref.MemberID || q.Board != ref.Board {
				continue
			}
			if q.BoardID != *in.JSON200.BoardId {
				return out, unknownQueue()
			}
			out.Queued.Messages = append(out.Queued.Messages, q)
			if limit > 0 && len(seat.msgs) >= limit {
				seat.More = true
				continue
			}
			after, n := api.Seq(q.Seq-1), 1
			rctx, cancel := context.WithTimeout(ctx, requestTimeout)
			got, readErr := c.api.ListMessagesWithResponse(rctx, ref.Board, &api.ListMessagesParams{After: &after, Limit: &n})
			cancel()
			if readErr != nil {
				return out, c.unreachable(readErr)
			}
			if got.JSON200 == nil {
				return out, apiError(got.StatusCode(), got.Body)
			}
			if len(got.JSON200.Messages) != 1 {
				return out, unknownQueue()
			}
			m := got.JSON200.Messages[0]
			if m.Id != q.MessageID || m.Seq != q.Seq || m.Board != ref.Board {
				return out, unknownQueue()
			}
			seat.msgs = append(seat.msgs, m)
			seat.Wrapped = append(seat.Wrapped, deliverytext.Format(textMessage(m), a.queuedContext(observation, ref)))
		}
		seat.Messages = cliMessages(seat.msgs)
		out.Seats = append(out.Seats, seat)
	}
	if !selected {
		return out, unknownQueue()
	}
	out.Queued.Count = len(out.Queued.Messages)
	return out, nil
}

func (a *app) runQueuedInbox(ctx context.Context, board, name string, limit int) error {
	observation, err := a.observeQueued(ctx)
	if err != nil {
		return err
	}
	if name == "" {
		name = a.env.Getenv("ABOARD_AGENT")
	}
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	out, err := a.queuedPreview(ctx, observation, creds, board, name, limit)
	if err != nil {
		return err
	}
	text := "Queued preview; still scheduled for turn end. Nothing acknowledged.\n" + queuedText(out.Queued)
	for _, seat := range out.Seats {
		text += seat.Server + " / " + seat.Board + " · @" + seat.Agent + "\n" + strings.Join(seat.Wrapped, "\n\n") + "\n"
	}
	if len(out.Seats) == 1 {
		seat := out.Seats[0]
		var bundle *string
		if len(seat.msgs) > 0 {
			var messages []deliverytext.Message
			for _, msg := range seat.msgs {
				messages = append(messages, textMessage(msg))
			}
			ref := delivery.AgentRef{Server: seat.Server, Board: seat.Board, Name: seat.Agent, MemberID: seat.MemberID}
			b := deliverytext.Bundle(seat.Board, messages, a.queuedContext(observation, ref))
			bundle = &b
		}
		a.emit(struct {
			Server    serverRef                `json:"server"`
			Board     string                   `json:"board"`
			Agent     string                   `json:"agent"`
			Messages  []cliMessage             `json:"messages"`
			AckedUpTo *int                     `json:"acked_up_to"`
			More      bool                     `json:"more"`
			Wrapped   []string                 `json:"wrapped"`
			Bundle    *string                  `json:"bundle"`
			Queued    *delivery.QueuedMessages `json:"queued"`
		}{a.serverRefFor(seat.Server), seat.Board, seat.Agent, seat.Messages, nil, seat.More, seat.Wrapped, bundle, out.Queued}, text)
	} else {
		a.emit(out, text)
	}
	return nil
}

func (a *app) statusQueued(ctx context.Context, server, memberID string) *delivery.QueuedMessages {
	observed, err := a.observeQueued(ctx, server)
	if err != nil {
		return nil
	}
	if server == "" {
		server, err = a.agentIssuer()
		if err != nil {
			return nil
		}
	}
	if memberID == "" {
		if server == "" {
			return observed.Queued
		}
		out := &delivery.QueuedMessages{Messages: []delivery.QueuedMessage{}}
		for _, message := range observed.Queued.Messages {
			if message.Server == server {
				out.Messages = append(out.Messages, message)
			}
		}
		out.Count = len(out.Messages)
		return out
	}
	found := false
	board := ""
	for _, ref := range observed.Agents {
		if ref.Server == server && ref.MemberID == memberID {
			found = true
			board = ref.Board
			break
		}
	}
	if !found {
		return nil
	}
	out := &delivery.QueuedMessages{Messages: []delivery.QueuedMessage{}}
	for _, message := range observed.Queued.Messages {
		if message.Server == server && message.Board == board && message.MemberID == memberID {
			out.Messages = append(out.Messages, message)
		}
	}
	out.Count = len(out.Messages)
	return out
}

func normalizeQueuedObservation(resp delivery.Response) (delivery.Response, error) {
	if resp.Queued == nil {
		return resp, unknownQueue()
	}
	issuers := map[string]bool{}
	for _, ref := range resp.Agents {
		issuers[ref.Server] = true
	}
	queue := *resp.Queued
	queue.Messages = append([]delivery.QueuedMessage(nil), queue.Messages...)
	for i := range queue.Messages {
		q := &queue.Messages[i]
		if q.Server == "" {
			if len(issuers) != 1 {
				return resp, unknownQueue()
			}
			for issuer := range issuers {
				q.Server = issuer
			}
		}
		matched := false
		for _, ref := range resp.Agents {
			if ref.Server == q.Server && ref.MemberID == q.MemberID && (q.Board == "" || q.Board == ref.Board) {
				q.Board = ref.Board
				matched = true
			}
		}
		if !matched {
			return resp, unknownQueue()
		}
	}
	resp.Queued = &queue
	return resp, nil
}

func (a *app) queuedContext(observation delivery.Response, ref delivery.AgentRef) deliverytext.Context {
	issuers := map[string]bool{}
	for _, seat := range observation.Agents {
		issuers[seat.Server] = true
	}
	issuer, _ := a.agentIssuer()
	c := deliverytext.Context{}
	if len(observation.Agents) > 1 || issuer != "" {
		c.Seat = ref.Name
		c.BoardQualified = true
	}
	if len(issuers) > 1 || issuer != "" {
		c.Server = ref.Server
	}
	return c
}
