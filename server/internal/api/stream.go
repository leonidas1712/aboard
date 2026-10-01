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
	if !s.send(rc, func() error { w.WriteHeader(http.StatusOK); return nil }) {
		return nil
	}
	for {
		heads, ticked, err := s.feed.Next(ctx, keepalive)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Error("stream: follow heads", "error", err)
			}
			return nil
		}
		var buf bytes.Buffer
		for _, hd := range heads {
			data, err := json.Marshal(headEvent{Board: hd.Board, BoardID: hd.BoardID, Seq: hd.Seq})
			if err != nil {
				s.log.Error("stream: encode head", "error", err)
				return nil
			}
			fmt.Fprintf(&buf, "event: head\ndata: %s\n\n", data)
		}
		if ticked {
			keepalive = s.clk.After(keepaliveEvery)
			buf.WriteString(": keepalive\n\n")
		}
		if buf.Len() > 0 && !s.send(rc, func() error { _, err := w.Write(buf.Bytes()); return err }) {
			return nil
		}
	}
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
