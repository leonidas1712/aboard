package cli

import (
	"context"
	"strconv"
)

// logoutUsage is the usage line of aboard logout.
const logoutUsage = "aboard logout --browsers [--json]"

// runLogout ends every browser login of the local owner, through the public API with
// the owner's own login. A browser login otherwise lasts 30 days, across restarts of
// the server.
func runLogout(ctx context.Context, a *app, args []string) error {
	fs := a.flags("logout")
	browsers := fs.Bool("browsers", false, "log every browser out")
	if _, err := a.parse(fs, args, logoutUsage, 0, 0); err != nil {
		return err
	}
	if !*browsers {
		return usageError("aboard logout needs --browsers, which logs every browser out.", logoutUsage)
	}
	if err := a.refuseInSession("Logging browsers out", "aboard logout --browsers"); err != nil {
		return err
	}
	srv := a.localServer()
	started, err := a.ensureLocal(ctx)
	if err != nil {
		return err
	}
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return err
	}
	r, err := c.api.EndBrowserTokensWithResponse(ctx, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	switch n := r.JSON200.Ended; n {
	case 0:
		text += "No browser was logged in.\n"
	case 1:
		text += "Logged out 1 browser. Run aboard open to log in again.\n"
	default:
		text += "Logged out " + strconv.Itoa(n) + " browsers. Run aboard open to log in again.\n"
	}
	a.emit(map[string]any{"server": srv, "server_started": started, "ended": r.JSON200.Ended}, text)
	return nil
}
