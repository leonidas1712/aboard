package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

// keepaliveEvery is how often a stream sends a comment, so proxies and clients can
// tell a quiet stream from a dead connection.
const keepaliveEvery = 25 * time.Second

// streamWriteWindow is how long one write to a stream may take. The server's
// WriteTimeout covers a whole response and would cut a long stream off, so the stream
// moves its own write deadline forward before every write instead: an open stream lives
// as long as the client keeps reading, and a client that stops reading is dropped
// within this window.
const streamWriteWindow = 30 * time.Second

// headEvent is the data of one `head` event, the HeadEvent schema in the spec.
type headEvent struct {
	Board   string `json:"board"`
	BoardID string `json:"board_id"`
	Seq     int64  `json:"seq"`
}

// presenceEvent is the data of one `presence` event, the PresenceEvent schema.
type presenceEvent struct {
	Board         string  `json:"board"`
	BoardID       string  `json:"board_id"`
	Agent         string  `json:"agent"`
	Presence      string  `json:"presence"`
	PresenceSince *string `json:"presence_since"`
}

// readEvent is the data of one `read` event, the ReadEvent schema.
type readEvent struct {
	Board    string `json:"board"`
	BoardID  string `json:"board_id"`
	Agent    string `json:"agent"`
	ReadUpTo int64  `json:"read_up_to"`
}

func presenceEventOf(pc board.PresenceChange) presenceEvent {
	return presenceEvent{Board: pc.Board, BoardID: pc.BoardID, Agent: pc.Agent, Presence: pc.Presence.State, PresenceSince: nullable(pc.Presence.Since)}
}

// writeEvent writes one server-sent event. Its data are plain structs of strings and
// numbers, which always encode.
func writeEvent(buf *bytes.Buffer, name string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(buf, "event: %s\ndata: %s\n\n", name, b)
}

// nullable returns nil for an empty string.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Stream answers GET /v1/stream. Which boards a human follows and when a head moved is
// decided by the board service; the response below only writes the events. A refusal
// (an agent token) is returned as an error before anything is written, so it gets the
// normal error response.
func (h *handlers) Stream(ctx context.Context, _ StreamRequestObject) (StreamResponseObject, error) {
	feed, err := h.svc.FollowHeads(principal(ctx))
	if err != nil {
		return nil, err
	}
	return headStream{ctx: ctx, shutdown: h.shutdown, feed: feed, clk: h.clk, log: h.log}, nil
}

// headStream writes the server-sent event stream. The strict server hands it the
// response writer and waits while it runs, so the stream is written as it happens
// rather than encoded once.
type headStream struct {
	ctx      context.Context // the request's
	shutdown context.Context // done when the server shuts down; may be nil
	feed     *board.HeadFeed
	clk      clock.Clock
	log      *slog.Logger
}

// VisitStreamResponse writes events until the client disconnects or the server shuts
// down. It always returns nil: once the status is written, a failure can only end the
// stream, and the client reconnects.
func (s headStream) VisitStreamResponse(w http.ResponseWriter) error {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	if s.shutdown != nil {
		stop := context.AfterFunc(s.shutdown, cancel)
		defer stop()
	}

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Each keepalive timer is started before the write that precedes it, so a client
	// that has read up to here knows the next one is already running.
	keepalive := s.clk.After(keepaliveEvery)
	// The starting point is read before the status is sent: a client that acts once it
	// sees the stream open is then sure its change comes as an event.
	first, err := s.feed.Start(ctx)
	if err != nil {
		s.ended(ctx, err)
		return nil
	}
	if !s.send(rc, func() error {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(streamEvents(first, false))
		return err
	}) {
		return nil
	}
	for {
		u, ticked, err := s.feed.Next(ctx, keepalive)
		if err != nil {
			s.ended(ctx, err)
			return nil
		}
		if ticked {
			keepalive = s.clk.After(keepaliveEvery)
		}
		if buf := streamEvents(u, ticked); len(buf) > 0 && !s.send(rc, func() error { _, err := w.Write(buf); return err }) {
			return nil
		}
	}
}

// ended logs why a stream ended, unless its credential stopped working (the client's
// next request gets 401) or the client went away.
func (s headStream) ended(ctx context.Context, err error) {
	if _, ended := apierr.As(err); !ended && ctx.Err() == nil {
		s.log.Error("stream: follow heads", "error", err)
	}
}

// streamEvents writes an update as server-sent events, with a keepalive comment after them
// when ticked.
func streamEvents(u board.Update, ticked bool) []byte {
	var buf bytes.Buffer
	for _, hd := range u.Heads {
		writeEvent(&buf, "head", headEvent{Board: hd.Board, BoardID: hd.BoardID, Seq: hd.Seq})
	}
	for _, pc := range u.Presence {
		writeEvent(&buf, "presence", presenceEventOf(pc))
	}
	for _, rc := range u.Reads {
		writeEvent(&buf, "read", readEvent{Board: rc.Board, BoardID: rc.BoardID, Agent: rc.Agent, ReadUpTo: rc.Cursor})
	}
	if ticked {
		buf.WriteString(": keepalive\n\n")
	}
	return buf.Bytes()
}

// send moves the write deadline forward, writes and flushes. It reports whether the
// stream can go on; a failed write means the client is gone.
func (s headStream) send(rc *http.ResponseController, write func() error) bool {
	// The deadline is enforced by the network connection against the system clock, so
	// it comes from time.Now; the injected clock only drives Aboard's own timers.
	err := rc.SetWriteDeadline(time.Now().Add(streamWriteWindow))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.log.Error("stream: set write deadline", "error", err)
		return false
	}
	if err := write(); err != nil {
		return false
	}
	return rc.Flush() == nil
}
