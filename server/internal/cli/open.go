package cli

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// openUsage is the usage line of aboard open.
var openUsage = usageOf("open")

// runOpen opens the web UI in the browser, signed in as the person, through a one-time
// login link: on the local server or a team server, the person's key never appears in
// a URL, and the link's code only in its fragment. The server is the one a person
// command picks: an explicit issuer or this machine's saved default. Inside a
// harness session, or with ABOARD_AGENT set, an agent runs it
// for its person, so it never shows the link there: with the code, the agent could log
// in before the browser and read the board as the person.
func runOpen(ctx context.Context, a *app, args []string) error {
	fs := a.flags("open")
	boardFlag := fs.String("board", "", "the board to show; without it, the board list")
	serverFlag := fs.String("server", "", "the server, when it isn't the one this machine would pick")
	pos, err := a.parse(fs, args, openUsage, 0, 1)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		if *serverFlag != "" {
			return usageError("Give the server as an argument or --server, not both.", openUsage)
		}
		*serverFlag = pos[0]
	}
	inSession := a.actsForAgent()
	srv, started, err := a.personServer(ctx, *serverFlag)
	if err != nil {
		e := asError(err)
		if e.Code == "server_not_selected" {
			if choices, ok := e.Details["server_choices"].([]map[string]string); ok && len(choices) > 0 {
				commands := make([]string, 0, len(choices))
				for _, choice := range choices {
					choice["command"] = "aboard open " + commandWord(choice["name"])
					commands = append(commands, choice["command"])
				}
				e.Hint = "Choose a server: " + strings.Join(commands, "; ") + "."
			}
		}
		return e
	}
	board := *boardFlag
	if p, ok, err := a.readProject(); err != nil {
		return err
	} else if board == "" && ok && (p.Server.URL == srv.URL || p.Server.URL == "" && srv.URL == a.localServer().URL) {
		board = p.Board
	}
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return err
	}
	if board != "" {
		if _, err := c.board(ctx, board); err != nil {
			return err
		}
	}
	r, err := c.api.CreateLoginCodeWithResponse(ctx, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	// The code goes in the fragment, which browsers never send to a server; the page
	// reads it, exchanges it for a browser session kept in a cookie its scripts can't
	// read, and drops it from the address bar.
	link := srv.URL + "/#code=" + url.QueryEscape(r.JSON201.Code)
	uiURL := srv.URL + "/"
	if board != "" {
		link += "&board=" + url.QueryEscape(board)
		uiURL += "?" + url.Values{"board": {board}}.Encode()
	}
	opened := a.env.OpenBrowser(ctx, link) == nil
	if !opened && inSession {
		cmd := "aboard open" + boardArg(a.namedBoard(*boardFlag))
		if *serverFlag != "" {
			cmd += " --server " + shellWord(srv.URL)
		}
		return newError("browser_unavailable", "Couldn't open a browser from this session.",
			"Give your human this command to run in their own terminal: "+cmd)
	}

	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	var linkOut, boardOut any
	switch {
	case inSession:
		shown := "Aboard"
		if board != "" {
			shown = board
		}
		text += "Opened " + shown + " in your browser: " + uiURL + "\n"
	case opened:
		linkOut = link
		text += "Opened " + link + " in your browser.\n"
	default:
		linkOut = link
		text += "Couldn't open a browser. Open this link in one within 60 seconds; it works once:\n  " + link + "\n"
	}
	if board != "" {
		boardOut = board
	}
	a.emit(map[string]any{
		"server": srv, "server_started": started, "board": boardOut, "ui_url": uiURL, "url": linkOut, "opened": opened,
		"expires_at": r.JSON201.ExpiresAt,
	}, text)
	return nil
}

// browserStartWait is how long openBrowser waits for the opener to fail before taking
// it as started. Openers such as open and xdg-open hand the URL over and exit; some
// setups run the browser itself in the foreground instead.
const browserStartWait = 5 * time.Second

// openBrowser opens link in the default browser: $BROWSER when set, else open on
// macOS and xdg-open elsewhere. It fails when the opener can't start or exits with an
// error.
func openBrowser(ctx context.Context, link string) error {
	opener := os.Getenv("BROWSER")
	if opener == "" {
		opener = "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
	}
	// Not tied to ctx: a browser the opener started in the foreground must outlive
	// this command.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), opener, link) //nolint:gosec // the opener is the person's own choice of browser
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	// The goroutine ends when the opener exits; if that is after this command
	// returns, the process exiting ends it.
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(browserStartWait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
