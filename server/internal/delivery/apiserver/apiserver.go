// Package apiserver connects the delivery daemon to an Aboard server through its public
// API: the server-sent event stream of board heads (GET /v1/stream) with the human's
// login, and each agent's inbox and acknowledgement with the agent's own token. It
// implements the delivery package's Server port. Tokens are read when a request needs
// them and sent only to the server they belong to.
package apiserver

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// Tokens finds this machine's logins for a server.
type Tokens interface {
	// AgentToken returns the agent's token, or delivery.ErrUnauthorized if this machine
	// has none.
	AgentToken(agent delivery.AgentRef) (string, error)
	// HumanToken returns the human login issued by the server at url, or
	// delivery.ErrLoginMissing.
	HumanToken(url string) (string, error)
}

// inboxPage is the most messages read per inbox request.
const inboxPage = 200

// streamSilence is how long the stream may go without a line before it counts as dead.
// The server sends a comment every 25 seconds.
const streamSilence = 75 * time.Second

// Server is one Aboard server.
type Server struct {
	url    string
	tokens Tokens
	rand   io.Reader
	http   *http.Client
	stream *http.Client
	// mu guards rand, which several goroutines may read through.
	mu sync.Mutex
}

var _ delivery.Server = (*Server)(nil)

// New returns the server at url. rand supplies idempotency keys.
func New(url string, tokens Tokens, rand io.Reader) *Server {
	return &Server{
		url: strings.TrimRight(url, "/"), tokens: tokens, rand: rand,
		http:   &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirects},
		stream: &http.Client{CheckRedirect: noRedirects},
	}
}

// noRedirects stops at a redirect instead of following it, so a token never goes to an
// address other than the server's own. The API never redirects.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func (s *Server) client(token string) (*api.ClientWithResponses, error) {
	c, err := api.NewClientWithResponses(s.url, api.WithHTTPClient(s.http),
		api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			if req.Method != http.MethodGet {
				key, err := s.idempotencyKey()
				if err != nil {
					return err
				}
				req.Header.Set("Idempotency-Key", key)
			}
			return nil
		}))
	if err != nil {
		return nil, fmt.Errorf("server address %s: %w", s.url, err)
	}
	return c, nil
}

func (s *Server) idempotencyKey() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := make([]byte, 16)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		return "", fmt.Errorf("read randomness for an idempotency key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// statusError turns an unexpected response into an error, marking a rejected token, and
// a board that answers board_not_found or a seat that answers agent_removed. An agent's
// requests here act only on its own board, so for them either answer means the agent
// can't reach its board, for good.
func statusError(what string, status int, body []byte) error {
	var w struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &w)
	detail := fmt.Sprintf("%d %s", status, http.StatusText(status))
	if w.Error.Code != "" {
		detail = w.Error.Code + ": " + w.Error.Message
	}
	// A removed seat is told so on every request; like a board that is gone, that is final.
	if (status == http.StatusNotFound && w.Error.Code == "board_not_found") ||
		(status == http.StatusForbidden && w.Error.Code == "agent_removed") {
		return fmt.Errorf("%s: %w (%s)", what, delivery.ErrBoardGone, detail)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("%s: %w (%s)", what, delivery.ErrUnauthorized, detail)
	}
	return fmt.Errorf("%s: %s", what, detail)
}

func (s *Server) inbox(ctx context.Context, agent delivery.AgentRef) (*api.Inbox, error) {
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return nil, err
	}
	c, err := s.client(token)
	if err != nil {
		return nil, err
	}
	limit := inboxPage
	r, err := c.GetInboxWithResponse(ctx, &api.GetInboxParams{Limit: &limit})
	if err != nil {
		return nil, fmt.Errorf("read inbox of %s on %s: %w", agent.Name, agent.Board, err)
	}
	if r.JSON200 == nil {
		return nil, statusError("read inbox of "+agent.Name+" on "+agent.Board, r.StatusCode(), r.Body)
	}
	if r.JSON200.Board != agent.Board || (agent.MemberID != "" && r.JSON200.MemberId != nil && *r.JSON200.MemberId != agent.MemberID) {
		return nil, fmt.Errorf("%w: inbox response identifies a different seat", delivery.ErrUnauthorized)
	}
	return r.JSON200, nil
}

// Inbox returns the agent's unread messages, oldest first, its read position and its
// delivery mode as the server holds it; mode is nil from a server that doesn't hold
// delivery modes.
func (s *Server) Inbox(ctx context.Context, agent delivery.AgentRef) (msgs []delivery.Message, cursor int, mode *delivery.HeldMode, err error) {
	in, err := s.inbox(ctx, agent)
	if err != nil {
		return nil, 0, nil, err
	}
	msgs = make([]delivery.Message, 0, len(in.Messages))
	for _, m := range in.Messages {
		msgs = append(msgs, TextMessage(m))
	}
	if in.DeliveryMode != nil {
		mode = &delivery.HeldMode{Mode: delivery.Mode(*in.DeliveryMode)}
		if in.DeliveryRevision != nil {
			mode.Revision = int64(*in.DeliveryRevision)
		}
	}
	return msgs, in.Cursor, mode, nil
}

// TaskWork reads only the task facts returned by the seat's inbox.
func (s *Server) TaskWork(ctx context.Context, agent delivery.AgentRef) (*deliverytext.TaskWork, error) {
	in, err := s.inbox(ctx, agent)
	if err != nil {
		return nil, err
	}
	if in.Work == nil {
		return nil, nil
	}
	w := in.Work
	out := &deliverytext.TaskWork{OpenTasks: w.OpenTasks, PostsWithoutTask: w.PostsWithoutTask, Nudges: w.Nudges}
	if w.OldestOpen != nil {
		out.OldestOpen = &deliverytext.TaskRef{ID: w.OldestOpen.Id, Ref: w.OldestOpen.Ref, Title: w.OldestOpen.Title}
	}
	if t := w.CurrentTask; t != nil {
		out.CurrentTask = &deliverytext.TaskContext{TaskRef: deliverytext.TaskRef{ID: t.Id, Ref: t.Ref, Title: t.Title}, Owner: t.Owner}
		if t.Stands != nil {
			out.CurrentTask.Stands = &deliverytext.TaskStands{Text: t.Stands.Text, By: t.Stands.By.Name, At: t.Stands.At, Version: t.Stands.Version}
			if t.Stands.MessagesSince != nil {
				out.CurrentTask.Stands.MessagesSince = *t.Stands.MessagesSince
			}
		}
	}
	return out, nil
}

// Ack moves the agent's read position up to upTo.
func (s *Server) Ack(ctx context.Context, agent delivery.AgentRef, upTo int) error {
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return err
	}
	c, err := s.client(token)
	if err != nil {
		return err
	}
	r, err := c.AckInboxWithResponse(ctx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: upTo})
	if err != nil {
		return fmt.Errorf("acknowledge %s on %s up to %d: %w", agent.Name, agent.Board, upTo, err)
	}
	if r.JSON200 == nil {
		return statusError(fmt.Sprintf("acknowledge %s on %s up to %d", agent.Name, agent.Board, upTo), r.StatusCode(), r.Body)
	}
	if agent.MemberID != "" && r.JSON200.MemberId != nil && *r.JSON200.MemberId != agent.MemberID {
		return fmt.Errorf("%w: acknowledgement response identifies a different seat", delivery.ErrUnauthorized)
	}
	return nil
}

// SetPresence reports what the agent's session is doing and its delivery mode, with the
// agent's own token. An empty mode leaves the mode on the server as it was.
func (s *Server) SetPresence(ctx context.Context, agent delivery.AgentRef, p delivery.Presence, mode delivery.Mode) error {
	token, err := s.tokens.AgentToken(agent)
	if err != nil {
		return err
	}
	c, err := s.client(token)
	if err != nil {
		return err
	}
	body := api.SetPresenceJSONRequestBody{Presence: api.Presence(p)}
	if mode != "" {
		m := api.DeliveryMode(mode)
		body.Delivery = &m
	}
	r, err := c.SetPresenceWithResponse(ctx, &api.SetPresenceParams{}, body)
	if err != nil {
		return fmt.Errorf("report presence of %s on %s: %w", agent.Name, agent.Board, err)
	}
	if r.JSON200 == nil {
		return statusError("report presence of "+agent.Name+" on "+agent.Board, r.StatusCode(), r.Body)
	}
	return nil
}

// Follow reads the server's stream of board heads until ctx ends or the stream fails.
func (s *Server) Follow(ctx context.Context, connected func(), head func(delivery.Head)) error {
	token, err := s.tokens.HumanToken(s.url)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+"/v1/stream", http.NoBody)
	if err != nil {
		return fmt.Errorf("stream from %s: %w", s.url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := s.stream.Do(req)
	if err != nil {
		return fmt.Errorf("stream from %s: %w", s.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return statusError("stream from "+s.url, resp.StatusCode, body)
	}
	connected()
	silence := time.AfterFunc(streamSilence, cancel)
	defer silence.Stop()
	observed := map[string]string{}
	return readEvents(resp.Body, func() { silence.Reset(streamSilence) }, func(event, data string) {
		var h struct {
			Board    string `json:"board"`
			BoardID  string `json:"board_id"`
			Seq      int    `json:"seq"`
			Agent    string `json:"agent"`
			MemberID string `json:"member_id"`
			ReadUpTo int    `json:"read_up_to"`
		}
		if json.Unmarshal([]byte(data), &h) != nil {
			return
		}
		switch {
		case event == "board_unavailable":
			// The id was observed on this connection. A normal head refresh rereads
			// current seat tokens; the hint itself changes no delivery or cursor state.
			if board := observed[h.BoardID]; board != "" {
				head(delivery.Head{Board: board})
			}
		case event == "head" && h.Board != "":
			if h.BoardID != "" {
				observed[h.BoardID] = h.Board
			}
			head(delivery.Head{Board: h.Board, Seq: h.Seq})
		case event == "read" && h.Board != "" && h.Agent != "":
			head(delivery.Head{Board: h.Board, Read: &delivery.ReadPosition{Agent: h.Agent, MemberID: h.MemberID, UpTo: h.ReadUpTo}})
		}
	})
}

// errStreamEnded means the server closed the stream.
var errStreamEnded = errors.New("the server closed the stream")

// readEvents parses a server-sent event stream, calling line for every line read and
// dispatch for every complete event.
func readEvents(r io.Reader, line func(), dispatch func(event, data string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1<<20)
	event, data := "", []string{}
	for sc.Scan() {
		line()
		text := sc.Text()
		switch {
		case text == "":
			if len(data) > 0 {
				name := event
				if name == "" {
					name = "message"
				}
				dispatch(name, strings.Join(data, "\n"))
			}
			event, data = "", data[:0]
		case strings.HasPrefix(text, ":"):
		default:
			field, value, _ := strings.Cut(text, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read stream: %w", err)
	}
	return errStreamEnded
}

// TextMessage turns an API message into what the delivery text shows of it.
func TextMessage(m api.Message) deliverytext.Message {
	t := deliverytext.Message{
		Board: m.Board, FromName: m.From.Name, FromHuman: m.From.Kind == "human",
		Sender: string(m.Sender), Seq: m.Seq, Urgent: m.Urgent, ExpectsReply: m.ExpectsReply, Body: m.Body,
	}
	if m.From.Owner != nil && m.ShowOwner {
		t.Owner = *m.From.Owner
	}
	if m.From.Role != nil {
		t.Role = *m.From.Role
	}
	if m.From.Harness != nil {
		t.Harness = *m.From.Harness
	}
	if m.ReplyToSeq != nil {
		t.ReplyToSeq = *m.ReplyToSeq
	}
	if m.ReplyToFrom != nil {
		t.ReplyToFrom = *m.ReplyToFrom
	}
	if m.About != nil {
		for _, tag := range *m.About {
			t.About = append(t.About, tag.Ref)
		}
	}
	for _, to := range m.To {
		t.To = append(t.To, string(to))
	}
	for _, mn := range m.Mentions {
		if mn.Wakes {
			t.Mentions = append(t.Mentions, mn.Name)
		}
	}
	for _, r := range m.Reactions {
		t.Reactions = append(t.Reactions, deliverytext.Reaction{Emoji: string(r.Emoji), Count: r.Count})
	}
	return t
}
