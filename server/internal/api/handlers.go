package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// handlers implements StrictServerInterface on top of the board service.
type handlers struct {
	svc     *board.Service
	version string
	// commit and commitTime are the server's build, reported by GET /v1/info.
	commit     string
	commitTime time.Time
	clk        clock.Clock
	log        *slog.Logger
	// shutdown is done when the server starts shutting down; event streams end then.
	shutdown context.Context
}

var _ StrictServerInterface = (*handlers)(nil)

const defaultLimit = 50

func limitOr(l *int) int {
	if l == nil {
		return defaultLimit
	}
	return *l
}

func afterOr(a *int) int64 {
	if a == nil {
		return 0
	}
	return int64(*a)
}

func (h *handlers) GetInfo(context.Context, GetInfoRequestObject) (GetInfoResponseObject, error) {
	cfg := h.svc.Config()
	info := GetInfo200JSONResponse{Name: "aboard", Version: h.version, ServerId: cfg.ServerID, Mode: ServerInfoMode(cfg.Mode)}
	if h.commit != "" {
		info.Commit = &h.commit
	}
	if !h.commitTime.IsZero() {
		info.CommitTime = &h.commitTime
	}
	return info, nil
}

func (h *handlers) CreateBoard(ctx context.Context, req CreateBoardRequestObject) (CreateBoardResponseObject, error) {
	in, err := convert[struct {
		Name     string `json:"name"`
		Title    string `json:"title"`
		Template string `json:"template"`
		Charter  string `json:"charter"`
		Preset   string `json:"preset"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.CreateBoard(ctx, principal(ctx), board.NewBoard{Name: in.Name, Title: in.Title, Template: in.Template, Charter: in.Charter, Preset: in.Preset})
	if err != nil {
		return nil, err
	}
	return convert[CreateBoard201JSONResponse](boardOf(v, principal(ctx)))
}

func (h *handlers) ListBoards(ctx context.Context, _ ListBoardsRequestObject) (ListBoardsResponseObject, error) {
	views, err := h.svc.ListBoards(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := struct {
		Boards []wireBoard `json:"boards"`
	}{Boards: []wireBoard{}}
	for _, v := range views {
		out.Boards = append(out.Boards, boardOf(v, principal(ctx)))
	}
	return convert[ListBoards200JSONResponse](out)
}

func (h *handlers) GetBoard(ctx context.Context, req GetBoardRequestObject) (GetBoardResponseObject, error) {
	v, err := h.svc.GetBoard(ctx, principal(ctx), req.Board)
	if err != nil {
		return nil, err
	}
	return convert[GetBoard200JSONResponse](boardOf(v, principal(ctx)))
}

func (h *handlers) UpdateBoard(ctx context.Context, req UpdateBoardRequestObject) (UpdateBoardResponseObject, error) {
	in, err := convert[struct {
		Title  *string             `json:"title"`
		Policy *rules.PolicyChange `json:"policy"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.UpdateBoard(ctx, principal(ctx), req.Board, board.Change{Title: in.Title, Policy: in.Policy})
	if err != nil {
		return nil, err
	}
	return convert[UpdateBoard200JSONResponse](boardOf(v, principal(ctx)))
}

func (h *handlers) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	me, err := h.svc.WhoAmI(ctx, principal(ctx))
	if err != nil {
		return nil, err
	}
	out := struct {
		ID      string  `json:"id"`
		Kind    string  `json:"kind"`
		Name    string  `json:"name"`
		Board   *string `json:"board"`
		Owner   *string `json:"owner"`
		Browser bool    `json:"browser"`
	}{Browser: me.Browser}
	if me.Agent != nil {
		out.ID, out.Kind, out.Name, out.Board, out.Owner = me.Agent.ID, "agent", me.Agent.Name, &me.Board, me.Agent.Owner
	} else {
		out.ID, out.Kind, out.Name = me.Human.ID, "human", me.Human.Name
	}
	return convert[GetMe200JSONResponse](out)
}

func (h *handlers) ListMembers(ctx context.Context, req ListMembersRequestObject) (ListMembersResponseObject, error) {
	ms, err := h.svc.Members(ctx, principal(ctx), req.Board)
	if err != nil {
		return nil, err
	}
	out := struct {
		Members []wireMember `json:"members"`
	}{Members: []wireMember{}}
	for _, m := range ms {
		out.Members = append(out.Members, memberOf(m, req.Board))
	}
	return convert[ListMembers200JSONResponse](out)
}

func (h *handlers) CreateJoinCode(ctx context.Context, req CreateJoinCodeRequestObject) (CreateJoinCodeResponseObject, error) {
	ttl := time.Duration(0)
	if req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	jc, err := h.svc.CreateJoinCode(ctx, principal(ctx), req.Board, req.Body.Role, ttl)
	if err != nil {
		return nil, err
	}
	return convert[CreateJoinCode201JSONResponse](wireJoinCode{
		ID: jc.JoinCode.ID, Code: jc.Code, JoinLine: jc.Line, Board: jc.Board, Role: jc.JoinCode.Role,
		ExpiresAt: jc.JoinCode.ExpiresAt, CreatedAt: jc.JoinCode.CreatedAt, CreatedBy: refOf(jc.Creator),
	})
}

func (h *handlers) RevokeJoinCode(ctx context.Context, req RevokeJoinCodeRequestObject) (RevokeJoinCodeResponseObject, error) {
	jc, creator, err := h.svc.RevokeJoinCode(ctx, principal(ctx), req.Board, req.JoinCode)
	if err != nil {
		return nil, err
	}
	return convert[RevokeJoinCode200JSONResponse](wireJoinCode{
		ID: jc.ID, Board: req.Board, Role: jc.Role, ExpiresAt: jc.ExpiresAt, CreatedAt: jc.CreatedAt,
		CreatedBy: refOf(creator), RevokedAt: jc.RevokedAt,
	})
}

func (h *handlers) Join(ctx context.Context, req JoinRequestObject) (JoinResponseObject, error) {
	in, err := convert[struct {
		Code    string `json:"code"`
		Board   string `json:"board"`
		Role    string `json:"role"`
		Name    string `json:"name"`
		Harness string `json:"harness"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	j, err := h.svc.Join(ctx, principal(ctx), board.JoinInput{Code: in.Code, Board: in.Board, Role: in.Role, Name: in.Name, Harness: in.Harness})
	if err != nil {
		return nil, err
	}
	return convert[Join201JSONResponse](struct {
		Agent wireMember `json:"agent"`
		Token string     `json:"token"`
		Board wireBoard  `json:"board"`
	}{memberOf(j.Agent, j.View.Board.Name), j.Token, boardOf(j.View, principal(ctx))})
}

func (h *handlers) PostMessage(ctx context.Context, req PostMessageRequestObject) (PostMessageResponseObject, error) {
	in, err := convert[struct {
		To           []string `json:"to"`
		Body         string   `json:"body"`
		ReplyTo      *string  `json:"reply_to"`
		Urgent       bool     `json:"urgent"`
		ExpectsReply bool     `json:"expects_reply"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	p := principal(ctx)
	m, err := h.svc.PostMessage(ctx, p, req.Board, board.NewMessage{To: in.To, Body: in.Body, ReplyTo: in.ReplyTo, Urgent: in.Urgent, ExpectsReply: in.ExpectsReply})
	if err != nil {
		return nil, err
	}
	// The sender is the reader of its own new message.
	out := messageOf(m, req.Board, board.Member{ID: m.SenderID})
	return convert[PostMessage201JSONResponse](out)
}

func (h *handlers) ListMessages(ctx context.Context, req ListMessagesRequestObject) (ListMessagesResponseObject, error) {
	q := req.Params
	f := board.TimelineFilter{After: afterOr(q.After), Limit: limitOr(q.Limit)}
	if q.Before != nil {
		f.Before = int64(*q.Before)
	}
	if q.Newest != nil {
		f.Newest = *q.Newest
	}
	if q.From != nil {
		f.From = *q.From
	}
	if q.Role != nil {
		f.Role = *q.Role
	}
	if q.ToMe != nil {
		f.ToMe = *q.ToMe
	}
	r, err := h.svc.Timeline(ctx, principal(ctx), req.Board, f)
	if err != nil {
		return nil, err
	}
	return convert[ListMessages200JSONResponse](struct {
		Board      string        `json:"board"`
		Messages   []wireMessage `json:"messages"`
		NextAfter  *int64        `json:"next_after"`
		PrevBefore *int64        `json:"prev_before"`
	}{r.Board.Name, messagesOf(r), r.NextAfter, r.PrevBefore})
}

func (h *handlers) GetInbox(ctx context.Context, req GetInboxRequestObject) (GetInboxResponseObject, error) {
	wait := time.Duration(0)
	if req.Params.Wait != nil {
		wait = time.Duration(*req.Params.Wait) * time.Second
	}
	r, more, err := h.svc.Inbox(ctx, principal(ctx), wait, afterOr(req.Params.After), limitOr(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	return convert[GetInbox200JSONResponse](struct {
		Board    string        `json:"board"`
		Agent    string        `json:"agent"`
		Messages []wireMessage `json:"messages"`
		Cursor   int64         `json:"cursor"`
		More     bool          `json:"more"`
	}{r.Board.Name, r.Reader.Name, messagesOf(r), r.Reader.Cursor, more})
}

func (h *handlers) AckInbox(ctx context.Context, req AckInboxRequestObject) (AckInboxResponseObject, error) {
	cursor, err := h.svc.Ack(ctx, principal(ctx), int64(req.Body.UpTo))
	if err != nil {
		return nil, err
	}
	return AckInbox200JSONResponse{Cursor: Seq(cursor)}, nil
}

func (h *handlers) SetPresence(ctx context.Context, req SetPresenceRequestObject) (SetPresenceResponseObject, error) {
	mode := ""
	if req.Body.Delivery != nil {
		mode = string(*req.Body.Delivery)
	}
	b, me, err := h.svc.SetPresence(ctx, principal(ctx), string(req.Body.Presence), mode)
	if err != nil {
		return nil, err
	}
	return convert[SetPresence200JSONResponse](map[string]string{
		"board": b.Name, "agent": me.Name, "presence": me.Presence.State, "presence_since": me.Presence.Since,
	})
}

func (h *handlers) ListEvents(ctx context.Context, req ListEventsRequestObject) (ListEventsResponseObject, error) {
	log, err := h.svc.Events(ctx, principal(ctx), req.Board, afterOr(req.Params.After), limitOr(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	evs := log.Events
	if evs == nil {
		evs = []events.Event{}
	}
	return convert[ListEvents200JSONResponse](struct {
		Board     string         `json:"board"`
		Events    []events.Event `json:"events"`
		HeadSeq   int64          `json:"head_seq"`
		NextAfter *int64         `json:"next_after"`
	}{log.Board.Name, evs, log.Board.HeadSeq, log.NextAfter})
}

// principal returns the caller authenticated by the auth middleware. Every operation
// except GetInfo runs behind it.
func principal(ctx context.Context) board.Principal {
	p, _ := ctx.Value(principalKey{}).(board.Principal)
	return p
}

type principalKey struct{}

// notImplemented is the answer to an operation in the spec that this server doesn't
// provide.
func notImplemented(what string) error {
	return apierr.New(http.StatusNotImplemented, "not_implemented", "This server doesn't provide "+what+".",
		"Read the board's messages with GET /v1/boards/{board}/messages (aboard read).")
}

func (h *handlers) GetMessage(context.Context, GetMessageRequestObject) (GetMessageResponseObject, error) {
	return nil, notImplemented("message status")
}

func (h *handlers) ListReplies(context.Context, ListRepliesRequestObject) (ListRepliesResponseObject, error) {
	return nil, notImplemented("lists of replies")
}
