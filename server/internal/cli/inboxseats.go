package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// inboxSeatsOutput is aboard inbox in a session with several seats and no --board
// (cli.yaml, InboxSeatsOutput): every seat's inbox, each read with its own token.
type inboxSeatsOutput struct {
	Seats []inboxSeat `json:"seats"`
	// Unavailable counts the seats whose inbox couldn't be read; they acknowledged
	// nothing and aren't named, since a refusal may come from a board they can't see.
	Unavailable int     `json:"unavailable"`
	Bundle      *string `json:"bundle"`
}

// inboxSeat is one seat's part of the inbox.
type inboxSeat struct {
	Server    string       `json:"server"`
	Board     string       `json:"board"`
	Agent     string       `json:"agent"`
	MemberID  string       `json:"member_id"`
	Messages  []cliMessage `json:"messages"`
	AckedUpTo *int         `json:"acked_up_to"`
	More      bool         `json:"more"`
	Wrapped   []string     `json:"wrapped"`

	msgs []api.Message
}

// seatReader is one seat with a client that sends its own token.
type seatReader struct {
	ref  delivery.AgentRef
	cred agentCredential
	c    *client
}

// inboxSeats reads the inbox of each of a session's seats, sorted by board, with each
// seat's own token. With ack it acknowledges, per seat, only up to the last message it
// shows; limit applies per seat. With wait (seconds) and nothing unread anywhere, it
// waits until any seat has a message. A seat whose read is refused is counted in
// Unavailable and acknowledges nothing.
func (a *app) inboxSeats(ctx context.Context, seats []delivery.AgentRef, creds credentials, limit int, ack bool, wait int) (inboxSeatsOutput, error) {
	out := inboxSeatsOutput{Seats: []inboxSeat{}}
	sorted := slices.Clone(seats)
	slices.SortFunc(sorted, func(x, y delivery.AgentRef) int { return strings.Compare(x.Board, y.Board) })
	timeout := time.Duration(wait)*time.Second + requestTimeout
	var readers []seatReader
	for _, ref := range sorted {
		cred, ok := seatCredential(creds, ref.Server, ref.MemberID)
		if !ok {
			out.Unavailable++
			continue
		}
		c, err := a.client(ctx, a.serverRefFor(ref.Server), cred.Token, timeout)
		if err != nil {
			return inboxSeatsOutput{}, err
		}
		readers = append(readers, seatReader{ref: ref, cred: cred, c: c})
	}
	if wait > 0 {
		readers = a.waitAnySeat(ctx, readers, wait, &out)
	}
	var groups []deliverytext.Group
	for _, r := range readers {
		in, msgs, acked, err := a.readInbox(ctx, r.c, r.ref, limit, ack, false)
		if err != nil {
			// The server refused this seat; one that can't be reached is so for every
			// seat, since they are all on one server.
			if code := asError(err).Code; code == "server_unreachable" || code == "sandbox_blocks_network" || code == "internal" {
				return inboxSeatsOutput{}, err
			}
			out.Unavailable++
			continue
		}
		seat := inboxSeat{
			Server: r.ref.Server, Board: in.Board, Agent: in.Agent, MemberID: r.cred.MemberID,
			Messages: cliMessages(msgs), AckedUpTo: acked, More: in.More, Wrapped: []string{}, msgs: msgs,
		}
		if in.MemberId != nil {
			seat.MemberID = *in.MemberId
		}
		var tms []deliverytext.Message
		for _, m := range msgs {
			seat.Wrapped = append(seat.Wrapped, deliveryText(m))
			tms = append(tms, textMessage(m))
		}
		groups = append(groups, deliverytext.Group{Board: in.Board, Messages: tms})
		out.Seats = append(out.Seats, seat)
	}
	if b := deliverytext.Bundles(groups); b != "" {
		out.Bundle = &b
	}
	return out, nil
}

// waitAnySeat waits, up to wait seconds, until any seat has an unread message, and
// returns the seats still readable; a seat whose wait is refused is counted unavailable.
func (a *app) waitAnySeat(ctx context.Context, readers []seatReader, wait int, out *inboxSeatsOutput) []seatReader {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(wait)*time.Second+requestTimeout)
	defer cancel()
	type result struct {
		i       int
		has     bool
		refused bool
	}
	results := make(chan result, len(readers))
	for i, r := range readers {
		go func() {
			r2, err := r.c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait, Limit: ptrTo(1)})
			switch {
			case err != nil:
				results <- result{i: i}
			case r2.JSON200 == nil:
				results <- result{i: i, refused: true}
			default:
				results <- result{i: i, has: len(r2.JSON200.Messages) > 0}
			}
		}()
	}
	refused := map[int]bool{}
	for range readers {
		res := <-results
		refused[res.i] = res.refused
		if res.has {
			break
		}
	}
	cancel()
	kept := readers[:0:0]
	for i, r := range readers {
		if refused[i] {
			out.Unavailable++
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// inboxSeatsText is the text output: one block per seat, each with its "<board> ·
// <count>" header line.
func inboxSeatsText(out inboxSeatsOutput) string {
	var b strings.Builder
	for i, s := range out.Seats {
		if i > 0 {
			b.WriteString("\n")
		}
		if len(s.msgs) == 0 {
			fmt.Fprintf(&b, "%s · no new messages\n", s.Board)
			continue
		}
		fmt.Fprintf(&b, "%s · %d new\n%s\n", s.Board, len(s.msgs), strings.Join(s.Wrapped, "\n\n"))
	}
	if out.Unavailable > 0 {
		fmt.Fprintf(&b, "%s couldn't be read; aboard status says which.\n", counted(out.Unavailable, "seat"))
	}
	return b.String()
}
