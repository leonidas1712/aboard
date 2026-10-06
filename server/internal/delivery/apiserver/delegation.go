package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Delegated reaches one server through the machine's delegation (spec/control.md, "The
// machine's delegation"): it lists the person's boards and joins sessions to them. It
// makes the delegation the first time it needs one, with the person's key for the
// server, and keeps its token in memory only; when the server says the delegation no
// longer works, it makes a new one once.
type Delegated struct {
	url    string
	name   string
	tokens Tokens
	http   *http.Client

	mu    sync.Mutex
	token string
}

// NewDelegated returns the server at url, reached through a delegation named name
// (the machine's name).
func NewDelegated(url, name string, tokens Tokens) *Delegated {
	return &Delegated{
		url: strings.TrimRight(url, "/"), name: name, tokens: tokens,
		http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirects},
	}
}

// wireRefusal is the API's error body.
type wireRefusal struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Hint    string         `json:"hint"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// send makes one request and returns its status and body. A request that fails or is
// answered with a 5xx is delivery.ErrServerUnreachable, so a lost answer is never taken
// for one.
func (d *Delegated) send(ctx context.Context, method, path, token string, body any) (status int, answer []byte, err error) {
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode %s %s: %w", method, path, err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.url+path, payload)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %s %s: %w", delivery.ErrServerUnreachable, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: read the answer to %s %s: %w", delivery.ErrServerUnreachable, method, path, err)
	}
	if resp.StatusCode >= 500 {
		return 0, nil, fmt.Errorf("%w: %s %s answered %d", delivery.ErrServerUnreachable, method, path, resp.StatusCode)
	}
	return resp.StatusCode, raw, nil
}

// refusal turns an error answer into what the command is told: the server's own
// refusal, or, for a server that doesn't have the route, delivery.ErrServerOutdated.
func refusal(status int, body []byte) error {
	var w wireRefusal
	_ = json.Unmarshal(body, &w)
	switch {
	case status == http.StatusNotImplemented, status == http.StatusNotFound && (w.Error.Code == "not_found" || w.Error.Code == ""):
		return delivery.ErrServerOutdated
	case w.Error.Code == "":
		return fmt.Errorf("%w: answered %d", delivery.ErrServerUnreachable, status)
	}
	return &delivery.WireError{Code: w.Error.Code, Message: w.Error.Message, Hint: w.Error.Hint, Details: w.Error.Details}
}

func delegationRevoked() error {
	return &delivery.WireError{
		Code:    "delegation_revoked",
		Message: "This machine's key for the server no longer works, so it can't list or join boards for a session.",
		Hint:    "Your person runs aboard login or aboard connect on this machine, then you run the command again.",
	}
}

// delegation returns the token to use: the one held, or a new one made with the
// person's key.
func (d *Delegated) delegation(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" {
		return d.token, nil
	}
	key, err := d.tokens.HumanToken(d.url)
	if err != nil {
		return "", err
	}
	status, body, err := d.send(ctx, http.MethodPost, "/v1/delegations", key, map[string]string{"name": d.name})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusCreated:
	case http.StatusUnauthorized:
		return "", delegationRevoked()
	default:
		return "", refusal(status, body)
	}
	var made struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &made); err != nil || made.Token == "" {
		return "", fmt.Errorf("%w: the new delegation's answer has no token", delivery.ErrServerUnreachable)
	}
	d.token = made.Token
	return d.token, nil
}

// drop forgets token, unless another request already replaced it.
func (d *Delegated) drop(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token == token {
		d.token = ""
	}
}

// call makes a request with the delegation, making a new delegation once when the
// server says the one held no longer works.
func (d *Delegated) call(ctx context.Context, method, path string, body any) (status int, answer []byte, err error) {
	for attempt := 0; ; attempt++ {
		token, err := d.delegation(ctx)
		if err != nil {
			return 0, nil, err
		}
		status, raw, err := d.send(ctx, method, path, token, body)
		if err != nil || status != http.StatusUnauthorized {
			return status, raw, err
		}
		d.drop(token)
		if attempt > 0 {
			return 0, nil, delegationRevoked()
		}
	}
}

// Boards lists the boards the person can see, filtered by lifecycle (empty asks for
// the server's default, active boards), with the server's archived count when it sent one.
func (d *Delegated) Boards(ctx context.Context, lifecycle string) (delivery.SeatBoards, error) {
	path := "/v1/boards"
	if lifecycle != "" {
		path += "?lifecycle=" + url.QueryEscape(lifecycle)
	}
	status, raw, err := d.call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return delivery.SeatBoards{}, err
	}
	if status != http.StatusOK {
		return delivery.SeatBoards{}, refusal(status, raw)
	}
	var list struct {
		Boards        []json.RawMessage `json:"boards"`
		ArchivedCount *int              `json:"archived_count"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return delivery.SeatBoards{}, fmt.Errorf("%w: the list of boards isn't JSON: %w", delivery.ErrServerUnreachable, err)
	}
	out := delivery.SeatBoards{Boards: make([]delivery.SeatBoard, 0, len(list.Boards)), ArchivedCount: list.ArchivedCount}
	for _, b := range list.Boards {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(b, &named); err != nil {
			return delivery.SeatBoards{}, fmt.Errorf("%w: a board isn't JSON: %w", delivery.ErrServerUnreachable, err)
		}
		out.Boards = append(out.Boards, delivery.SeatBoard{Name: named.Name, Board: b})
	}
	return out, nil
}

// Join asks the server for the session's seat on a board.
func (d *Delegated) Join(ctx context.Context, req delivery.SeatRequest) (delivery.SeatGrant, error) {
	body := map[string]string{"board": req.Board, "session": req.Session}
	for k, v := range map[string]string{"role": req.Role, "name": req.Name, "harness": req.Harness} {
		if v != "" {
			body[k] = v
		}
	}
	status, raw, err := d.call(ctx, http.MethodPost, "/v1/join", body)
	if err != nil {
		return delivery.SeatGrant{}, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return delivery.SeatGrant{}, refusal(status, raw)
	}
	var joined struct {
		Agent  json.RawMessage `json:"agent"`
		Token  string          `json:"token"`
		Board  json.RawMessage `json:"board"`
		Reused bool            `json:"reused"`
	}
	var agent struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Board        string `json:"board"`
		DeliveryMode string `json:"delivery_mode"`
	}
	if err := json.Unmarshal(raw, &joined); err != nil {
		return delivery.SeatGrant{}, fmt.Errorf("%w: the join's answer isn't JSON: %w", delivery.ErrServerUnreachable, err)
	}
	if err := json.Unmarshal(joined.Agent, &agent); err != nil || agent.ID == "" || joined.Token == "" {
		return delivery.SeatGrant{}, fmt.Errorf("%w: the join's answer has no seat or token", delivery.ErrServerUnreachable)
	}
	if !strings.HasPrefix(agent.ID, "mem_") {
		return delivery.SeatGrant{}, errors.New("the join's answer names a seat that isn't a member id")
	}
	return delivery.SeatGrant{
		Seat:  delivery.SeatRef{Server: d.url, Board: agent.Board, Name: agent.Name, MemberID: agent.ID},
		Token: joined.Token, Reused: joined.Reused, Board: joined.Board, Member: joined.Agent, Mode: delivery.Mode(agent.DeliveryMode),
	}, nil
}
