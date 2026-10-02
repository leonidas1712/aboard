package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
)

// watchUsage is the usage line of aboard watch.
const watchUsage = "aboard watch [--from @NAME] [--role R] [--limit N] [--board NAME] [--json]"

// defaultWatchLimit is how many earlier messages aboard watch shows before following.
const defaultWatchLimit = 20

// watchPage is the most messages watch reads per request while catching up.
const watchPage = 200

// runWatch follows a board live with the human login: it prints the newest matching
// messages, then each new one as the server's event stream says the board moved. It
// reads past addressed visibility, so it refuses inside a harness session.
func runWatch(ctx context.Context, a *app, args []string) error {
	fs := a.flags("watch")
	from := fs.String("from", "", "only messages from this member (@name)")
	role := fs.String("role", "", "only messages from members with this role")
	limit := fs.Int("limit", defaultWatchLimit, "how many earlier messages to show first")
	boardFlag := fs.String("board", "", "the board to watch")
	if _, err := a.parse(fs, args, watchUsage, 0, 0); err != nil {
		return err
	}
	if *limit < 1 {
		return usageError("--limit must be at least 1.", watchUsage)
	}
	if err := a.refuseInSession("Watching a board as yourself", "aboard watch"+boardArg(*boardFlag)); err != nil {
		return err
	}
	t, err := a.selectBoard(*boardFlag)
	if err != nil {
		return err
	}
	token, err := a.readOwnerToken(t.server)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, t.server, token, requestTimeout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w := &watchRun{a: a, c: c, board: t.board, filter: readFilter{from: strings.TrimPrefix(*from, "@"), role: *role}}
	first := w.filter.params()
	newest := true
	first.Newest, first.Limit = &newest, limit
	page, err := w.fetch(ctx, first)
	if err != nil {
		return err
	}
	w.print(page)
	_, _ = fmt.Fprintf(a.env.Stderr, "Watching %s. Stop with Ctrl-C.\n", t.board)

	wake := make(chan struct{}, 1)
	poke := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	srv := apiserver.New(t.server.URL, watchLogin{url: strings.TrimRight(t.server.URL, "/"), token: token}, a.env.Rand)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return w.follow(gctx, srv, poke) })
	g.Go(func() error {
		for {
			select {
			case <-gctx.Done():
				return nil
			case <-wake:
				if err := w.catchUp(gctx); err != nil && gctx.Err() == nil {
					// The stream has most likely dropped too; reconnecting wakes this
					// loop again, and reading resumes after the last message printed.
					_, _ = fmt.Fprintf(a.env.Stderr, "Couldn't read new messages: %s\n", asError(err).Message)
				}
			}
		}
	})
	err = g.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// watchRun is one aboard watch. last is only touched by the goroutine that prints.
type watchRun struct {
	a      *app
	c      *client
	board  string
	filter readFilter
	last   int
}

// fetch reads one page of the board's matching messages.
func (w *watchRun) fetch(ctx context.Context, p api.ListMessagesParams) (*api.MessagePage, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return w.c.messages(ctx, w.board, p)
}

// catchUp prints every matching message after the last one printed.
func (w *watchRun) catchUp(ctx context.Context) error {
	for {
		p := w.filter.params()
		after, size := w.last, watchPage
		p.After, p.Limit = &after, &size
		page, err := w.fetch(ctx, p)
		if err != nil {
			return err
		}
		w.print(page)
		if page.NextAfter == nil {
			return nil
		}
	}
}

func (w *watchRun) print(page *api.MessagePage) {
	for _, m := range page.Messages {
		if w.a.json {
			enc := json.NewEncoder(w.a.env.Stdout)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(struct {
				Board   string      `json:"board"`
				Message api.Message `json:"message"`
			}{page.Board, m})
		} else {
			_, _ = io.WriteString(w.a.env.Stdout, timelineText([]api.Message{m}))
		}
		w.last = m.Seq
	}
}

// follow keeps the server's event stream open, reconnecting with backoff, and pokes
// whenever the board's head moves or the stream (re)opens, so nothing posted while it
// was down is missed.
func (w *watchRun) follow(ctx context.Context, srv *apiserver.Server, poke func()) error {
	clk := clock.Real{}
	failures := 0
	for {
		err := srv.Follow(ctx, func() {
			if failures > 0 {
				_, _ = fmt.Fprintf(w.a.env.Stderr, "Reconnected to %s.\n", w.c.server.URL)
			}
			failures = 0
			poke()
		}, func(h delivery.Head) {
			if h.Board == w.board {
				poke()
			}
		})
		select {
		case <-ctx.Done(): // Ctrl-C ended the stream; that is not a failure.
			return nil
		default:
		}
		if errors.Is(err, delivery.ErrUnauthorized) {
			return newError("login_required", "The server at "+w.c.server.URL+" refused the local owner login.",
				"Run aboard doctor to check the local server and its login.")
		}
		failures++
		wait := watchBackoff(failures)
		_, _ = fmt.Fprintf(w.a.env.Stderr, "Lost the connection to %s; reconnecting in %s.\n", w.c.server.URL, wait)
		select {
		case <-ctx.Done():
			return nil
		case <-clk.After(wait):
		}
	}
}

// watchBackoff waits 1 second after the first failure, doubling up to 30.
func watchBackoff(failures int) time.Duration {
	d := time.Second << min(failures-1, 5)
	return min(d, 30*time.Second)
}

// watchLogin gives the event stream the human login, and only to the server it is for.
type watchLogin struct{ url, token string }

func (l watchLogin) AgentToken(delivery.AgentRef) (string, error) {
	return "", delivery.ErrUnauthorized
}

func (l watchLogin) HumanToken(url string) (string, error) {
	if url != l.url {
		return "", delivery.ErrLoginMissing
	}
	return l.token, nil
}
