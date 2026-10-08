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
)

func unknownQueue() *Error {
	return &Error{Code: "queue_unknown", Message: "This session's queue could not be verified.", Hint: "Use the current session with a running delivery daemon; nothing was acknowledged."}
}

// observeQueued never starts a daemon or sends a prompt, claim or receipt.
func (a *app) observeQueued(ctx context.Context) (delivery.Response, error) {
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
	req := delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpQueued, Harness: key.Harness, Session: key.ID, Boot: a.env.Getenv("ABOARD_BOOT")}
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
	return resp, nil
}

func queuedText(q *delivery.QueuedMessages) string {
	if q == nil || q.Count == 0 {
		return ""
	}
	boards := map[string]bool{}
	for _, m := range q.Messages {
		boards[m.BoardID] = true
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
		names = append(names, label)
	}
	return fmt.Sprintf("%d queued, arriving at the end of this turn: %s\n", q.Count, strings.Join(names, ", "))
}

func (a *app) queuedPreview(ctx context.Context, observation delivery.Response, creds credentials, board, name string, limit int) (inboxSeatsOutput, error) {
	out := inboxSeatsOutput{Seats: []inboxSeat{}, Queued: &delivery.QueuedMessages{Messages: []delivery.QueuedMessage{}}}
	selected := false
	for _, ref := range observation.Agents {
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
			if q.MemberID != ref.MemberID {
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
			seat.Wrapped = append(seat.Wrapped, deliveryText(m))
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
		text += seat.Board + " · @" + seat.Agent + "\n" + strings.Join(seat.Wrapped, "\n\n") + "\n"
	}
	if len(out.Seats) == 1 {
		seat := out.Seats[0]
		var bundle *string
		if len(seat.msgs) > 0 {
			b := bundleText(seat.Board, seat.msgs)
			bundle = &b
		}
		a.emit(struct {
			Board     string                   `json:"board"`
			Agent     string                   `json:"agent"`
			Messages  []cliMessage             `json:"messages"`
			AckedUpTo *int                     `json:"acked_up_to"`
			More      bool                     `json:"more"`
			Wrapped   []string                 `json:"wrapped"`
			Bundle    *string                  `json:"bundle"`
			Queued    *delivery.QueuedMessages `json:"queued"`
		}{seat.Board, seat.Agent, seat.Messages, nil, seat.More, seat.Wrapped, bundle, out.Queued}, text)
	} else {
		a.emit(out, text)
	}
	return nil
}

func (a *app) statusQueued(ctx context.Context, server, memberID string) *delivery.QueuedMessages {
	observed, err := a.observeQueued(ctx)
	if err != nil {
		return nil
	}
	if memberID == "" {
		return observed.Queued
	}
	found := false
	for _, ref := range observed.Agents {
		if ref.Server == server && ref.MemberID == memberID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	out := &delivery.QueuedMessages{Messages: []delivery.QueuedMessage{}}
	for _, message := range observed.Queued.Messages {
		if message.MemberID == memberID {
			out.Messages = append(out.Messages, message)
		}
	}
	out.Count = len(out.Messages)
	return out
}
