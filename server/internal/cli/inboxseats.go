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
	observations []*shownRead
	holds        []*inboxRead
	Queued       *delivery.QueuedMessages `json:"queued,omitempty"`
	Seats        []inboxSeat              `json:"seats"`
	// Unavailable counts the seats whose inbox couldn't be read; they acknowledged
	// nothing and aren't named, since a refusal may come from a board they can't see.
	Unavailable int `json:"unavailable"`
	// Unacknowledged counts the seats whose messages were shown but whose acknowledgement
	// failed; those messages come again.
	Unacknowledged int     `json:"unacknowledged"`
	Bundle         *string `json:"bundle"`
}

// inboxSeat is one seat's part of the inbox.
type inboxSeat struct {
	Server    string               `json:"server"`
	Board     string               `json:"board"`
	Agent     string               `json:"agent"`
	MemberID  string               `json:"member_id"`
	Messages  []cliMessage         `json:"messages"`
	AckedUpTo *int                 `json:"acked_up_to"`
	More      bool                 `json:"more"`
	Wrapped   []string             `json:"wrapped"`
	Work      *api.AgentWork       `json:"work,omitempty"`
	Nudges    []deliverytext.Nudge `json:"nudges"`

	msgs []api.Message
	// reader and read are the seat's client and every message its read returned,
	// which its acknowledgement covers.
	reader seatReader
	read   []api.Message
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
func (a *app) inboxSeatsHeld(ctx context.Context, seats []delivery.AgentRef, creds credentials, limit int, ack bool, wait int) (inboxSeatsOutput, error) {
	out := inboxSeatsOutput{Seats: []inboxSeat{}}
	success := false
	defer func() {
		if !success {
			out.done()
		}
	}()
	issuers := map[string]bool{}
	for _, seat := range seats {
		issuers[seat.Server] = true
	}
	multiIssuer := len(issuers) > 1
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
	// Every seat is read first, with the daemon holding its deliveries until the end, so
	// nothing shown here is also handed to the session. A seat whose read fails is only
	// counted, whatever the reason: it may be a board the person can't see now.
	var groups []deliverytext.Group
	var read []*inboxSeat
	var shown []api.Message

	for _, r := range readers {
		rd := a.startInboxRead(ctx, r.ref, false)
		out.holds = append(out.holds, rd)
		params := &api.GetInboxParams{}
		if limit > 0 {
			params.Limit = &limit
		}
		res, err := r.c.api.GetInboxWithResponse(ctx, params)
		if err != nil || res.JSON200 == nil {
			out.Unavailable++
			continue
		}
		in := res.JSON200
		out.observations = append(out.observations, a.inboxShown(r.ref, in, rd))
		msgs := rd.unread(in.Messages)
		seat := &inboxSeat{
			Server: r.ref.Server, Board: in.Board, Agent: in.Agent, MemberID: r.cred.MemberID,
			Messages: cliMessages(msgs), More: in.More, Wrapped: []string{}, msgs: msgs, reader: r, read: in.Messages,
		}
		if in.MemberId != nil {
			seat.MemberID = *in.MemberId
		}
		seat.Work = in.Work
		seat.Nudges = a.taskNudges(ctx, r.c, r.ref, in, "inbox", true)
		dc := deliverytext.Context{BoardQualified: len(seats) > 1}
		if multiIssuer {
			dc.Server = r.ref.Server
		}
		var tms []deliverytext.Message
		for _, m := range msgs {
			seat.Wrapped = append(seat.Wrapped, deliverytext.Format(textMessage(m), dc))
			tms = append(tms, textMessage(m))
		}
		shown = append(shown, msgs...)
		groups = append(groups, deliverytext.Group{Board: in.Board, Messages: tms, Context: dc})
		read = append(read, seat)
	}
	// Then each seat read is acknowledged up to the last message it read. One whose
	// acknowledgement fails is still shown; its messages come again.
	for _, seat := range read {
		if ack && len(seat.read) > 0 {
			upTo := seat.read[len(seat.read)-1].Seq
			res, err := seat.reader.c.api.AckInboxWithResponse(ctx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: upTo})
			if err == nil && res.JSON200 != nil {
				seat.AckedUpTo = &res.JSON200.Cursor
			} else {
				out.Unacknowledged++
			}
		}
		out.Seats = append(out.Seats, *seat)
	}
	if b := deliverytext.Bundles(groups); b != "" {
		out.Bundle = &b
	}
	if len(read) == 0 && len(readers) > 0 && len(shown) == 0 {
		// Nothing could be read at all: say why, as a single-seat inbox would.
		if _, err := readers[0].c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Limit: ptrTo(1)}); err != nil {
			return inboxSeatsOutput{}, readers[0].c.unreachable(err)
		}
	}
	success = true
	return out, nil
}

// waitAnySeat waits, up to wait seconds, until any seat has an unread message, and
// returns the seats still readable; a seat whose wait is refused is counted unavailable.
func (a *app) waitAnySeat(ctx context.Context, readers []seatReader, wait int, out *inboxSeatsOutput) []seatReader {
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
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
			r2, err := r.c.waitInbox(ctx, deadline, api.GetInboxParams{Wait: &wait, Limit: ptrTo(1)})
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
	issuers := map[string]bool{}
	for _, seat := range out.Seats {
		issuers[seat.Server] = true
	}
	multi := len(issuers) > 1
	for i, s := range out.Seats {
		if i > 0 {
			b.WriteString("\n")
		}
		board := s.Board
		if multi {
			board = s.Server + " · " + board
		}
		if len(s.msgs) == 0 {
			fmt.Fprintf(&b, "%s · no new messages\n", board)
			b.WriteString(nudgesText(s.Nudges))
			continue
		}
		fmt.Fprintf(&b, "%s · %d new\n%s%s\n", board, len(s.msgs), nudgesText(s.Nudges), strings.Join(s.Wrapped, "\n\n"))
	}
	if out.Unavailable > 0 {
		fmt.Fprintf(&b, "%s couldn't be read; aboard status says which.\n", counted(out.Unavailable, "seat"))
	}
	if out.Unacknowledged > 0 {
		fmt.Fprintf(&b, "%s couldn't be marked read; their messages will come again.\n", counted(out.Unacknowledged, "seat"))
	}
	return b.String()
}

func (out *inboxSeatsOutput) done() {
	for _, hold := range out.holds {
		hold.done()
	}
}

func (out *inboxSeatsOutput) report(ctx context.Context) {
	for i, seat := range out.Seats {
		if i < len(out.observations) {
			out.observations[i].report(ctx, seat.msgs)
		}
	}
}

func (a *app) inboxSeats(ctx context.Context, seats []delivery.AgentRef, creds credentials, limit int, ack bool, wait int) (inboxSeatsOutput, error) {
	out, err := a.inboxSeatsHeld(ctx, seats, creds, limit, ack, wait)
	out.done()
	return out, err
}
