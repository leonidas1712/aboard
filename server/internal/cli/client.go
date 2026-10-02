package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// requestTimeout bounds every API call except a waiting inbox read.
const requestTimeout = 30 * time.Second

// client calls one server's API with one token.
type client struct {
	api    *api.ClientWithResponses
	server serverRef
}

// client returns an API client for srv that sends token, and a fresh Idempotency-Key
// on every write. token may be empty for calls that need no login.
func (a *app) client(srv serverRef, token string, timeout time.Duration) (*client, error) {
	rnd := a.env.Rand
	c, err := api.NewClientWithResponses(srv.URL,
		api.WithHTTPClient(&http.Client{Timeout: timeout}),
		api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			if req.Method != http.MethodGet && req.Header.Get("Idempotency-Key") == "" {
				key, err := idempotencyKey(rnd)
				if err != nil {
					return err
				}
				req.Header.Set("Idempotency-Key", key)
			}
			return nil
		}))
	if err != nil {
		return nil, newError("invalid_request", "The server address "+srv.URL+" is not a valid URL.",
			"Fix the server URL in the project's .aboard file.")
	}
	return &client{api: c, server: srv}, nil
}

func idempotencyKey(rnd io.Reader) (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rnd, b); err != nil {
		return "", fmt.Errorf("read randomness for an idempotency key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// unreachable reports a request that got no answer from the server.
func (c *client) unreachable(err error) *Error {
	return &Error{
		Code:    "server_unreachable",
		Message: "Couldn't reach the Aboard server at " + c.server.URL + ".",
		Hint:    "Start the local server with aboard up, then run the command again.",
		Err:     err,
	}
}

// info returns the server's identity.
func (c *client) info(ctx context.Context) (*api.ServerInfo, error) {
	r, err := c.api.GetInfoWithResponse(ctx)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}

// board returns a board by name.
func (c *client) board(ctx context.Context, name string) (*api.Board, error) {
	r, err := c.api.GetBoardWithResponse(ctx, name)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}

// messages returns a page of a board's timeline.
func (c *client) messages(ctx context.Context, board string, params api.ListMessagesParams) (*api.MessagePage, error) {
	r, err := c.api.ListMessagesWithResponse(ctx, board, &params)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}
