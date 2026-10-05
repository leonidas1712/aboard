package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// keysUsage is the usage of "aboard keys".
var keysUsage = usageOf("keys")

// runKeys runs "aboard keys", which lists a person's access keys, and "aboard keys
// create|revoke". Keys are a person's own: every form refuses inside a harness session,
// and the server refuses agent and browser tokens.
func runKeys(ctx context.Context, a *app, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		return runKeysList(ctx, a, args)
	case "create":
		return runKeysCreate(ctx, a, args)
	case "revoke":
		return runKeysRevoke(ctx, a, args)
	case "sessions":
		return runKeysSessions(ctx, a, args)
	}
	return usageError("aboard keys has no command "+strconv.Quote(sub)+".", keysUsage)
}

// keysServer is the server keys commands act on: --server, else the one this
// directory's .aboard names, else the local server, which it starts if needed. It
// reports whether it started it.
func (a *app) keysServer(ctx context.Context, flag string) (serverRef, bool, error) {
	srv := a.localServer()
	if flag != "" {
		srv = a.serverRefFor(strings.TrimSuffix(strings.TrimSpace(flag), "/"))
	} else if p, ok, err := a.readProject(); err != nil {
		return serverRef{}, false, err
	} else if ok && p.Server.URL != "" {
		srv = p.Server
	}
	if srv.URL != a.localServer().URL {
		return srv, false, nil
	}
	started, err := a.ensureLocal(ctx)
	return srv, started, err
}

// keysClient returns a client for srv that sends the key this machine keeps for it.
func (a *app) keysClient(ctx context.Context, srv serverRef) (*client, error) {
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return nil, err
	}
	return a.client(ctx, srv, token, requestTimeout)
}

// keyRejected is the error for a request the server refused because this machine's key
// no longer works.
func keyRejected(srv serverRef, status int, body []byte) *Error {
	e := apiError(status, body)
	if status != http.StatusUnauthorized {
		return e
	}
	e.Message = "This machine's key for " + srv.URL + " no longer works: it was revoked or has expired."
	e.Hint = "Paste a working key with aboard login " + srv.URL + "."
	return e
}

func runKeysList(ctx context.Context, a *app, args []string) error {
	fs := a.flags("keys")
	person := fs.String("person", "", "another person whose keys to list; admins only")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	if _, err := a.parse(fs, args, keysUsage, 0, 0); err != nil {
		return err
	}
	if err := a.refuseInSession("Managing access keys", "aboard keys"); err != nil {
		return err
	}
	srv, started, err := a.keysServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	list, err := c.keys(ctx, *person)
	if err != nil {
		return err
	}
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += a.keysText(srv, list)
	a.emit(map[string]any{
		"server": srv, "person": list.Person, "keys": list.Keys, "current_key_id": list.CurrentKeyId,
	}, text)
	return nil
}

// keys lists a person's keys, the caller's own when person is empty.
func (c *client) keys(ctx context.Context, person string) (*api.AccessKeyList, error) {
	var params api.ListKeysParams
	if person != "" {
		params.Person = &person
	}
	r, err := c.api.ListKeysWithResponse(ctx, &params)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, keyRejected(c.server, r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}

// keysText lists keys one per line: name, state, last use, expiry and what depends on
// each.
func (a *app) keysText(srv serverRef, list *api.AccessKeyList) string {
	st := a.out()
	now := time.Now()
	text := fmt.Sprintf("Keys of %s on %s:\n", st.name(list.Person.Handle), srv.URL)
	rows := make([][]string, 0, len(list.Keys))
	for _, k := range list.Keys {
		row := []string{k.Name}
		switch {
		case k.State != nil && *k.State == api.AccessKeyStateRevoked && k.RevokedAt != nil:
			row = append(row, "revoked "+agoText(*k.RevokedAt, now))
		case k.State != nil && *k.State == api.AccessKeyStateExpired:
			row = append(row, "expired")
		default:
			row = append(row, "working", lastUsedText(k.LastUsedAt, now), expiryText(k, now))
			var extra []string
			if n := count(k.BrowserSessions); n > 0 {
				extra = append(extra, fmt.Sprintf("browser sessions: %d", n))
			}
			if n := count(k.AgentSeats); n > 0 {
				extra = append(extra, fmt.Sprintf("agents: %d", n))
			}
			if k.Id == list.CurrentKeyId {
				extra = append(extra, "(this machine)")
			}
			row = append(row, strings.Join(extra, "  "))
		}
		rows = append(rows, row)
	}
	return text + swarmTable(st, rows, func(r []string) []string { return r })
}

func lastUsedText(t *time.Time, now time.Time) string {
	if t == nil {
		return "never used"
	}
	return "last used " + agoText(*t, now)
}

// expiryText says when a key expires: "expires in 88 days", "expires when unused for 90
// days" or "never expires".
func expiryText(k api.AccessKey, now time.Time) string {
	switch {
	case k.IdleExpirySeconds != nil:
		return "expires when unused for " + durationText(time.Duration(*k.IdleExpirySeconds)*time.Second)
	case k.ExpiresAt == nil:
		return "never expires"
	}
	return "expires in " + daysText(k.ExpiresAt.Sub(now))
}

// daysText is a duration in whole days, or in hours under two days.
func daysText(d time.Duration) string {
	if d < 48*time.Hour {
		return durationText(d)
	}
	return fmt.Sprintf("%d days", int((d+12*time.Hour)/(24*time.Hour)))
}

// parseExpiry reads --expires: a number of days, weeks or years (90d, 2w, 1y), or a
// duration such as 12h.
func parseExpiry(s string) (time.Duration, error) {
	units := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour, 'y': 365 * 24 * time.Hour}
	if len(s) > 1 {
		if unit, ok := units[s[len(s)-1]]; ok {
			n, err := strconv.Atoi(s[:len(s)-1])
			if err != nil || n <= 0 {
				return 0, errors.New("bad number")
			}
			return time.Duration(n) * unit, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errors.New("bad duration")
	}
	return d, nil
}

func runKeysCreate(ctx context.Context, a *app, args []string) error {
	fs := a.flags("keys")
	expires := fs.String("expires", "", "how long the key works, such as 90d, 12h or 1y; default 90d")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	pos, err := a.parse(fs, args, keysUsage, 1, 1)
	if err != nil {
		return err
	}
	if err := a.refuseInSession("Creating an access key", "aboard keys create "+shellWord(pos[0])); err != nil {
		return err
	}
	req := api.CreateKeyRequest{Name: pos[0]}
	if *expires != "" {
		d, err := parseExpiry(*expires)
		if err != nil {
			return usageError("--expires takes a duration such as 90d, 12h or 1y.", keysUsage)
		}
		secs := int(d.Seconds())
		req.TtlSeconds = &secs
	}
	srv, started, err := a.keysServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	r, err := c.api.CreateKeyWithResponse(ctx, nil, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	k := r.JSON201
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += fmt.Sprintf("Key %q (shown once, then never again): %s\n", k.Name, k.Token)
	text += "Save it in your password manager. Anyone with it can sign in as you until you revoke it.\n"
	if k.ExpiresAt != nil {
		text += fmt.Sprintf("It expires on %s (in %s). ", k.ExpiresAt.Format("2006-01-02"), daysText(time.Until(*k.ExpiresAt)))
	}
	text += "Revoke it with: aboard keys revoke " + k.Name + "\n"
	a.emit(map[string]any{"server": srv, "key": k}, text)
	return nil
}

func runKeysRevoke(ctx context.Context, a *app, args []string) error {
	fs := a.flags("keys")
	person := fs.String("person", "", "another person whose key to revoke; admins only")
	yes := fs.Bool("yes", false, "revoke this machine's own key without asking")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	pos, err := a.parse(fs, args, keysUsage, 1, 1)
	if err != nil {
		return err
	}
	again := "aboard keys revoke " + shellWord(pos[0])
	if *person != "" {
		again += " --person " + shellWord(*person)
	}
	if err := a.refuseInSession("Revoking an access key", again); err != nil {
		return err
	}
	srv, started, err := a.keysServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	list, err := c.keys(ctx, *person)
	if err != nil {
		return err
	}
	k, err := pickKey(list, pos[0])
	if err != nil {
		return err
	}
	mine := k.Id == list.CurrentKeyId
	if mine && !*yes {
		if !a.interactive() {
			return newError("confirmation_required",
				k.Name+" is the key this machine uses for "+srv.URL+": revoking it signs this machine out, with every agent it started.",
				"Run "+again+" --yes to revoke it anyway, or revoke it from another machine.")
		}
		ok, err := a.asker().confirm("Revoke "+k.Name+", this machine's own key?",
			"This machine is signed out of "+srv.URL+", and its agents' seats stop working. Sign in again with aboard login.", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			a.emit(nil, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	r, err := c.api.RevokeKeyWithResponse(ctx, k.Id, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	got := r.JSON200
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += "Revoked " + got.Name + "."
	var ended []string
	if n := count(got.BrowserSessions); n > 0 {
		ended = append(ended, counted(n, "browser session"))
	}
	if n := count(got.AgentSeats); n > 0 {
		ended = append(ended, counted(n, "agent"))
	}
	if len(ended) > 0 {
		text += " Its " + strings.Join(ended, " and ") + " stopped working with it."
	}
	text += "\n"
	if mine {
		text += "This machine is signed out of " + srv.URL + ". Sign in again with: aboard login " + srv.URL + "\n"
	}
	a.emit(map[string]any{"server": srv, "person": list.Person.Handle, "key": got, "this_machine": mine}, text)
	return nil
}

// count reads an optional count, 0 when absent.
func count(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// runKeysSessions runs "aboard keys sessions [<key>]", which lists the browsers signed in
// as the person, and "aboard keys sessions end <id>", which signs one of them out.
func runKeysSessions(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 && args[0] == "end" {
		return runKeysSessionsEnd(ctx, a, args[1:])
	}
	fs := a.flags("keys")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	pos, err := a.parse(fs, args, keysUsage, 0, 1)
	if err != nil {
		return err
	}
	again := "aboard keys sessions"
	if len(pos) == 1 {
		again += " " + shellWord(pos[0])
	}
	if err := a.refuseInSession("Listing browser sessions", again); err != nil {
		return err
	}
	srv, started, err := a.keysServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	var params api.ListBrowserSessionsParams
	var keyName string
	if len(pos) == 1 {
		keys, err := c.keys(ctx, "")
		if err != nil {
			return err
		}
		k, err := pickKey(keys, pos[0])
		if err != nil {
			return err
		}
		params.Key, keyName = &k.Id, k.Name
	}
	r, err := c.api.ListBrowserSessionsWithResponse(ctx, &params)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	list := r.JSON200
	st := a.out()
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	switch {
	case len(list.Sessions) == 0 && keyName != "":
		text += "No browser is signed in to " + srv.URL + " with the key " + keyName + ".\n"
	case len(list.Sessions) == 0:
		text += "No browser is signed in to " + srv.URL + " as " + st.name(list.Person.Handle) + ".\n"
	default:
		text += fmt.Sprintf("Browser sessions of %s on %s:\n", st.name(list.Person.Handle), srv.URL)
		now := time.Now()
		rows := make([][]string, 0, len(list.Sessions))
		for _, s := range list.Sessions {
			how := "aboard open"
			if s.StartedWith == api.BrowserSessionStartAccessKey {
				how = "pasted key"
			}
			rows = append(rows, []string{s.Id, "key " + s.Key.Name, how, "signed in " + agoText(s.CreatedAt, now), "ends in " + daysText(s.ExpiresAt.Sub(now))})
		}
		text += swarmTable(st, rows, func(r []string) []string { return r })
		text += "Sign one out with: aboard keys sessions end <id>\n"
	}
	a.emit(map[string]any{"server": srv, "person": list.Person, "sessions": list.Sessions}, text)
	return nil
}

// runKeysSessionsEnd signs one browser out by its session's id.
func runKeysSessionsEnd(ctx context.Context, a *app, args []string) error {
	fs := a.flags("keys")
	serverFlag := fs.String("server", "", "the server, when it isn't this directory's or the local one")
	pos, err := a.parse(fs, args, keysUsage, 1, 1)
	if err != nil {
		return err
	}
	if err := a.refuseInSession("Ending a browser session", "aboard keys sessions end "+shellWord(pos[0])); err != nil {
		return err
	}
	if !strings.HasPrefix(pos[0], "ses_") {
		return usageError("aboard keys sessions end takes a session's id, starting with ses_, as aboard keys sessions lists it.", keysUsage)
	}
	srv, started, err := a.keysServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	r, err := c.api.EndBrowserSessionWithResponse(ctx, pos[0], nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	text += "Signed out browser session " + r.JSON200.Id + " (key " + r.JSON200.Key.Name + "). The key and its other sessions keep working.\n"
	a.emit(map[string]any{"server": srv, "session": r.JSON200}, text)
	return nil
}

// counted says "1 agent" or "2 agents".
func counted(n int, what string) string {
	return strconv.Itoa(n) + " " + plural(n, what, what+"s")
}

// pickKey finds the key a person named, by id or by name. A name two keys share picks
// the one that works, if exactly one does.
func pickKey(list *api.AccessKeyList, ref string) (api.AccessKey, error) {
	var named []api.AccessKey
	for _, k := range list.Keys {
		if k.Id == ref {
			return k, nil
		}
		if k.Name == ref {
			named = append(named, k)
		}
	}
	if len(named) > 1 {
		var working []api.AccessKey
		for _, k := range named {
			if k.State != nil && *k.State == api.AccessKeyStateWorking {
				working = append(working, k)
			}
		}
		named = working
	}
	if len(named) == 1 {
		return named[0], nil
	}
	choices := make([]map[string]string, 0, len(list.Keys))
	for _, k := range list.Keys {
		choices = append(choices, map[string]string{"name": k.Name, "id": k.Id})
	}
	msg := list.Person.Handle + " has no key called " + ref + "."
	if len(named) > 1 {
		msg = list.Person.Handle + " has several working keys called " + ref + "."
	}
	e := newError("key_not_selected", msg, "Name one of the keys aboard keys lists, by name or by id.")
	e.Details = map[string]any{"choices": choices}
	return api.AccessKey{}, e
}
