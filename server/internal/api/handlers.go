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
	features := []string{"tasks", "asks"}
	if cfg.Blobs != nil {
		features = append(features, "files")
		info.Storage = &struct {
			Db    string `json:"db"`
			Files string `json:"files"`
		}{Db: "sqlite", Files: "disk"}
	}
	info.Features = &features
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
		Name       string `json:"name"`
		Title      string `json:"title"`
		Template   string `json:"template"`
		Charter    string `json:"charter"`
		Preset     string `json:"preset"`
		Visibility string `json:"visibility"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.CreateBoard(ctx, principal(ctx), board.NewBoard{Name: in.Name, Title: in.Title, Template: in.Template, Charter: in.Charter, Preset: in.Preset, Visibility: in.Visibility})
	if err != nil {
		return nil, err
	}
	return convert[CreateBoard201JSONResponse](boardOf(v, principal(ctx)))
}

func (h *handlers) ListBoards(ctx context.Context, req ListBoardsRequestObject) (ListBoardsResponseObject, error) {
	all := req.Params.All != nil && *req.Params.All
	lifecycle := "active"
	if req.Params.Lifecycle != nil {
		lifecycle = string(*req.Params.Lifecycle)
	}
	list, err := h.svc.ListBoards(ctx, principal(ctx), all, lifecycle)
	if err != nil {
		return nil, err
	}
	out := struct {
		Boards        []wireBoard       `json:"boards"`
		Hidden        *[]map[string]any `json:"hidden_boards,omitempty"`
		ArchivedCount int               `json:"archived_count"`
	}{Boards: []wireBoard{}, ArchivedCount: list.ArchivedCount}
	for _, v := range list.Boards {
		out.Boards = append(out.Boards, boardOf(v, principal(ctx)))
	}
	if principal(ctx).Delegation != nil {
		// A delegation never gets the person's own read position or questions, and no
		// hidden boards, whatever all says.
		return convert[ListBoards200JSONResponse](struct {
			Boards        []wireBoard `json:"boards"`
			ArchivedCount int         `json:"archived_count"`
		}{Boards: out.Boards, ArchivedCount: list.ArchivedCount})
	}
	if all {
		hidden := []map[string]any{}
		for _, hb := range list.Hidden {
			hidden = append(hidden, map[string]any{
				"id": hb.ID, "visibility": board.BoardPrivate, "created_at": hb.CreatedAt,
				"created_by": map[string]string{"id": hb.Creator.ID, "handle": hb.Creator.Name}, "people": hb.People,
				"lifecycle": hb.Lifecycle, "can_archive": hb.CanArchive, "can_restore": hb.CanRestore, "can_delete": hb.CanDelete,
			})
		}
		out.Hidden = &hidden
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
		AgentsAddPeople *bool               `json:"agents_add_people"`
		Title           *string             `json:"title"`
		Policy          *rules.PolicyChange `json:"policy"`
		TaskPrefix      *string             `json:"task_prefix"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	v, err := h.svc.UpdateBoard(ctx, principal(ctx), req.Board, board.Change{Title: in.Title, Policy: in.Policy, AgentsAddPeople: in.AgentsAddPeople, TaskPrefix: in.TaskPrefix})
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
		CurrentTask any     `json:"current_task"`
		ID          string  `json:"id"`
		Kind        string  `json:"kind"`
		Name        string  `json:"name"`
		Board       *string `json:"board"`
		Owner       *string `json:"owner"`
		Browser     bool    `json:"browser"`
		ServerRole  *string `json:"server_role"`
		DisplayName *string `json:"display_name"`
		// DeliveryMode and DeliveryRevision are an agent's; null for a person.
		DeliveryMode     *string `json:"delivery_mode"`
		DeliveryRevision *int64  `json:"delivery_revision"`
	}{Browser: me.Browser}
	if me.Agent != nil {
		out.CurrentTask = taskRefOf(me.Agent.CurrentTask)
		out.ID, out.Kind, out.Name, out.Board, out.Owner = me.Agent.ID, "agent", me.Agent.Name, &me.Board, me.Agent.Owner
		mode, rev := me.Agent.Delivery.Current(), me.Agent.Delivery.Seq
		out.DeliveryMode, out.DeliveryRevision = &mode, &rev
	} else {
		out.ID, out.Kind, out.Name = me.Human.ID, "human", me.Human.Name
		out.ServerRole, out.DisplayName = &me.Human.Role, me.Human.DisplayName
	}
	return convert[GetMe200JSONResponse](out)
}

func (h *handlers) ListMembers(ctx context.Context, req ListMembersRequestObject) (ListMembersResponseObject, error) {
	removed := req.Params.Removed != nil && *req.Params.Removed
	l, err := h.svc.ListMembers(ctx, principal(ctx), req.Board, removed)
	if err != nil {
		return nil, err
	}
	out := struct {
		Members []wireMember `json:"members"`
	}{Members: []wireMember{}}
	for _, m := range l.Members {
		w := memberOf(m, req.Board)
		if can, ok := l.CanRemove[m.ID]; ok {
			w.CanRemove = &can
		}
		if m.Status != board.StatusActive {
			w.RemovedAt, w.RemovedBy = m.RemovedAt, m.RemovedBy
			w.Presence, w.PresenceSince = nil, nil
		}
		out.Members = append(out.Members, w)
	}
	return convert[ListMembers200JSONResponse](out)
}

func (h *handlers) CreateJoinCode(ctx context.Context, req CreateJoinCodeRequestObject) (CreateJoinCodeResponseObject, error) {
	ttl := time.Duration(0)
	if req.Body.TtlSeconds != nil {
		ttl = time.Duration(*req.Body.TtlSeconds) * time.Second
	}
	in := board.JoinCodeInput{Role: req.Body.Role, TTL: ttl}
	if req.Body.Guest != nil {
		in.Guest = *req.Body.Guest
	}
	jc, err := h.svc.CreateJoinCode(ctx, principal(ctx), req.Board, in)
	if err != nil {
		return nil, err
	}
	return convert[CreateJoinCode201JSONResponse](wireJoinCode{
		ID: jc.JoinCode.ID, Kind: jc.JoinCode.Kind, Guest: jc.JoinCode.Guest,
		Code: jc.Code, JoinLine: jc.Line, Board: jc.Board, Role: jc.JoinCode.Role,
		ExpiresAt: jc.JoinCode.ExpiresAt, CreatedAt: jc.JoinCode.CreatedAt, CreatedBy: refOf(jc.Creator),
	})
}

func (h *handlers) RevokeJoinCode(ctx context.Context, req RevokeJoinCodeRequestObject) (RevokeJoinCodeResponseObject, error) {
	jc, creator, err := h.svc.RevokeJoinCode(ctx, principal(ctx), req.Board, req.JoinCode)
	if err != nil {
		return nil, err
	}
	return convert[RevokeJoinCode200JSONResponse](wireJoinCode{
		ID: jc.ID, Kind: jc.Kind, Guest: jc.Guest, Board: req.Board, Role: jc.Role, ExpiresAt: jc.ExpiresAt, CreatedAt: jc.CreatedAt,
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
		Session string `json:"session"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	j, err := h.svc.Join(ctx, principal(ctx), board.JoinInput{Code: in.Code, Board: in.Board, Role: in.Role, Name: in.Name, Harness: in.Harness, Session: in.Session})
	if err != nil {
		return nil, err
	}
	body, err := convert[JoinResult](joinedOf(j, principal(ctx)))
	if err != nil {
		return nil, err
	}
	// The answer holds the agent's token, which no cache may keep.
	noStore := "no-store"
	if j.Reused {
		return Join200JSONResponse{Body: body, Headers: Join200ResponseHeaders{CacheControl: &noStore}}, nil
	}
	return Join201JSONResponse{Body: body, Headers: Join201ResponseHeaders{CacheControl: &noStore}}, nil
}

// joinedOf is a new agent with its token and board, as the agent sees the board.
func joinedOf(j board.Joined, p board.Principal) any {
	out := struct {
		Agent  wireMember `json:"agent"`
		Token  string     `json:"token"`
		Board  wireBoard  `json:"board"`
		Reused *bool      `json:"reused,omitempty"`
	}{Agent: memberOf(j.Agent, j.View.Board.Name), Token: j.Token, Board: boardOf(j.View, p)}
	if j.Reused {
		out.Reused = &j.Reused
	}
	return out
}

// GuestJoin redeems a guest code. It needs no token: the code is the proof.
func (h *handlers) GuestJoin(ctx context.Context, req GuestJoinRequestObject) (GuestJoinResponseObject, error) {
	in := board.GuestJoinInput{Code: req.Body.Code, KeyName: req.Body.KeyName}
	if req.Body.Name != nil {
		in.Name = *req.Body.Name
	}
	if req.Body.Harness != nil {
		in.Harness = *req.Body.Harness
	}
	g, err := h.svc.GuestJoin(ctx, in)
	if err != nil {
		return nil, err
	}
	k := g.Key
	return convert[GuestJoin201JSONResponse](map[string]any{
		"server_id": h.svc.Config().ServerID, "person": personOf(g.Person),
		"key": map[string]any{
			"id": k.ID, "name": k.Name, "created_at": k.CreatedAt, "expires_at": k.ExpiresAt,
			"idle_expiry_seconds": k.IdleSeconds, "token": g.KeyToken,
		},
		"agent": memberOf(g.Agent, g.View.Board.Name), "token": g.Token,
		"board": boardOf(g.View, board.Principal{Agent: &g.Agent}),
	})
}

func (h *handlers) PostMessage(ctx context.Context, req PostMessageRequestObject) (PostMessageResponseObject, error) {
	in, err := convert[struct {
		Ask          *board.NewAsk        `json:"ask"`
		Answer       *board.NewAnswer     `json:"answer"`
		Files        []board.FileSelector `json:"files"`
		About        *[]string            `json:"about"`
		To           []string             `json:"to"`
		Body         string               `json:"body"`
		ReplyTo      *string              `json:"reply_to"`
		Urgent       bool                 `json:"urgent"`
		ExpectsReply bool                 `json:"expects_reply"`
	}](req.Body)
	if err != nil {
		return nil, err
	}
	p := principal(ctx)
	m, err := h.svc.PostMessage(ctx, p, req.Board, board.NewMessage{Files: in.Files, Ask: in.Ask, Answer: in.Answer, About: in.About, To: in.To, Body: in.Body, ReplyTo: in.ReplyTo, Urgent: in.Urgent, ExpectsReply: in.ExpectsReply})
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
	if q.Task != nil {
		f.Task = *q.Task
	}
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

func (h *handlers) AddReaction(ctx context.Context, req AddReactionRequestObject) (AddReactionResponseObject, error) {
	r, err := h.svc.React(ctx, principal(ctx), req.Message, string(req.Reaction), true)
	if err != nil {
		return nil, err
	}
	return convert[AddReaction200JSONResponse](messagesOf(r)[0])
}

func (h *handlers) RemoveReaction(ctx context.Context, req RemoveReactionRequestObject) (RemoveReactionResponseObject, error) {
	r, err := h.svc.React(ctx, principal(ctx), req.Message, string(req.Reaction), false)
	if err != nil {
		return nil, err
	}
	return convert[RemoveReaction200JSONResponse](messagesOf(r)[0])
}

func (h *handlers) ListThreads(ctx context.Context, req ListThreadsRequestObject) (ListThreadsResponseObject, error) {
	l, err := h.svc.Threads(ctx, principal(ctx), req.Board, limitOr(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	type thread struct {
		Root         wireMessage `json:"root"`
		Participants []string    `json:"participants"`
	}
	roots := make([]board.Message, 0, len(l.Threads))
	for _, t := range l.Threads {
		roots = append(roots, t.Root)
	}
	out := struct {
		Board   string   `json:"board"`
		Threads []thread `json:"threads"`
		More    bool     `json:"more"`
	}{Board: l.Board.Name, Threads: []thread{}, More: l.More}
	for i, m := range messagesOf(board.Reading{Board: l.Board, Reader: l.Reader, Messages: roots}) {
		out.Threads = append(out.Threads, thread{Root: m, Participants: l.Threads[i].Participants})
	}
	return convert[ListThreads200JSONResponse](out)
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
		Work             any           `json:"work"`
		BoardID          string        `json:"board_id"`
		Board            string        `json:"board"`
		Agent            string        `json:"agent"`
		MemberID         string        `json:"member_id"`
		Messages         []wireMessage `json:"messages"`
		Cursor           int64         `json:"cursor"`
		More             bool          `json:"more"`
		DeliveryMode     string        `json:"delivery_mode"`
		DeliveryRevision int64         `json:"delivery_revision"`
		MidturnPolicy    string        `json:"midturn_policy"`
	}{taskWorkOf(r.Work), r.Board.ID, r.Board.Name, r.Reader.Name, r.Reader.ID, messagesOf(r), r.Reader.Cursor, more, r.Reader.Delivery.Current(), r.Reader.Delivery.Seq, r.MidturnPolicy})
}

func (h *handlers) SetDeliveryMode(ctx context.Context, req SetDeliveryModeRequestObject) (SetDeliveryModeResponseObject, error) {
	c, err := h.svc.SetDeliveryMode(ctx, principal(ctx), req.Board, req.Member, string(req.Body.Mode))
	if err != nil {
		return nil, err
	}
	return SetDeliveryMode200JSONResponse{
		Board: c.Board.Name, Agent: c.Agent.Name, Mode: DeliveryModeSetting(c.Agent.Delivery.Current()),
		Revision: int(c.Agent.Delivery.Seq), Changed: c.Changed,
	}, nil
}

func (h *handlers) AckInbox(ctx context.Context, req AckInboxRequestObject) (AckInboxResponseObject, error) {
	cursor, err := h.svc.Ack(ctx, principal(ctx), int64(req.Body.UpTo))
	if err != nil {
		return nil, err
	}
	out := AckInbox200JSONResponse{Cursor: Seq(cursor)}
	// The seat whose cursor this is: the token's member id.
	if p := principal(ctx); p.Agent != nil {
		out.MemberId = &p.Agent.ID
	}
	return out, nil
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

// CreateDelegation makes a machine's delegation with the caller's own access key.
func (h *handlers) CreateDelegation(ctx context.Context, req CreateDelegationRequestObject) (CreateDelegationResponseObject, error) {
	d, err := h.svc.CreateDelegation(ctx, principal(ctx), req.Body.Name)
	if err != nil {
		return nil, err
	}
	// The answer holds the delegation's token, which no cache may keep.
	body, err := convert[NewDelegation](map[string]string{
		"id": d.Delegation.ID, "name": d.Delegation.Name, "key_id": d.Delegation.KeyID, "created_at": d.Delegation.CreatedAt, "token": d.Token,
	})
	if err != nil {
		return nil, err
	}
	noStore := "no-store"
	return CreateDelegation201JSONResponse{Body: body, Headers: CreateDelegation201ResponseHeaders{CacheControl: &noStore}}, nil
}

func (h *handlers) ListReplies(ctx context.Context, req ListRepliesRequestObject) (ListRepliesResponseObject, error) {
	wait := time.Duration(0)
	if req.Params.Wait != nil {
		wait = time.Duration(*req.Params.Wait) * time.Second
	}
	th, err := h.svc.Thread(ctx, principal(ctx), req.Message, wait, afterOr(req.Params.After), limitOr(req.Params.Limit))
	if err != nil {
		return nil, err
	}
	out := struct {
		MessageID string        `json:"message_id"`
		Root      *wireMessage  `json:"root"`
		Replies   []wireMessage `json:"replies"`
		NextAfter *int64        `json:"next_after"`
	}{MessageID: req.Message, Replies: []wireMessage{}, NextAfter: th.NextAfter}
	r := board.Reading{Board: th.Board, Reader: th.Reader, Messages: th.Replies}
	if th.Root != nil {
		r.Messages = append([]board.Message{*th.Root}, th.Replies...)
	}
	ms := messagesOf(r)
	if th.Root != nil {
		out.Root, ms = &ms[0], ms[1:]
	}
	out.Replies = append(out.Replies, ms...)
	return convert[ListReplies200JSONResponse](out)
}
