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
	// sandbox is the harness whose sandbox blocks this command's network access, if any.
	sandbox string
}

// client returns an API client for srv that sends token, and a fresh Idempotency-Key
// on every write. token may be empty for calls that need no login. The first time a
// command uses the local server, a local server from an older aboard is replaced.
func (a *app) client(ctx context.Context, srv serverRef, token string, timeout time.Duration) (*client, error) {
	if srv.URL == a.localServer().URL {
		if err := a.replaceOutdatedLocal(ctx); err != nil {
			return nil, err
		}
	}
	return a.newClient(srv, token, timeout)
}

// newClient returns an API client like client, without checking the server's build.
func (a *app) newClient(srv serverRef, token string, timeout time.Duration) (*client, error) {
	rnd := a.env.Rand
	c, err := api.NewClientWithResponses(srv.URL,
		api.WithHTTPClient(&http.Client{Timeout: timeout, Transport: &outdatedServer{
			base: http.DefaultTransport, srv: srv, local: srv.URL == a.localServer().URL,
		}, CheckRedirect: noRedirects}),
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
	sandbox, _ := a.networkBlocked()
	return &client{api: c, server: srv, sandbox: sandbox}, nil
}

// noRedirects stops the client at a redirect instead of following it. The API never
// redirects, and following one would carry the request's key to wherever it points;
// the redirect's response becomes the command's error.
func noRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func idempotencyKey(rnd io.Reader) (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rnd, b); err != nil {
		return "", fmt.Errorf("read randomness for an idempotency key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// unreachable reports a request that got no answer from the server.
func (c *client) unreachable(err error) *Error {
	if c.sandbox != "" {
		e := sandboxBlocksNetwork(c.sandbox, "the Aboard server at "+c.server.URL)
		e.Err = err
		return e
	}
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

// thread returns the whole thread a message is in: its first message, when the caller
// may see it, and every reply, oldest first.
func (c *client) thread(ctx context.Context, id string) (*api.ReplyPage, error) {
	out := &api.ReplyPage{MessageId: id, Replies: []api.Message{}}
	after, limit := 0, 200
	for {
		r, err := c.api.ListRepliesWithResponse(ctx, id, &api.ListRepliesParams{After: &after, Limit: &limit})
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, apiError(r.StatusCode(), r.Body)
		}
		out.Root = r.JSON200.Root
		out.Replies = append(out.Replies, r.JSON200.Replies...)
		if r.JSON200.NextAfter == nil {
			return out, nil
		}
		after = *r.JSON200.NextAfter
	}
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
