package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// loginUsage is the usage line of aboard login.
const loginUsage = "aboard login [<server URL>] [--json]   (the key on standard input)"

// runLogin saves an access key a person pastes, for one server: the argument, else the
// one this directory's .aboard names, else the local server. The key is read from
// standard input, never from the command line, and checked with the server before it is
// saved. It is up to a person, so it refuses inside a harness session.
func runLogin(ctx context.Context, a *app, args []string) error {
	fs := a.flags("login")
	pos, err := a.parse(fs, args, loginUsage, 0, 1)
	if err != nil {
		return err
	}
	if err := a.refuseInSession("Signing this machine in", "aboard login"); err != nil {
		return err
	}
	target := a.localServer().URL
	if len(pos) == 1 {
		target = pos[0]
	} else if p, ok, err := a.readProject(); err != nil {
		return err
	} else if ok && p.Server.URL != "" {
		target = p.Server.URL
	}
	// Wherever the server came from, it is checked before a key is read or sent: https,
	// or this machine over http.
	srv, err := parseServerURL(target)
	if err != nil {
		return err
	}
	srv = a.serverRefFor(srv.URL)
	local := srv.URL == a.localServer().URL
	key, err := a.readKey(srv)
	if err != nil {
		return err
	}
	invalid := newError("key_invalid", "That isn't an access key for "+srv.URL+": it is wrong, revoked or expired.",
		"Paste a working key, which starts with abh_; make one with aboard keys create <name> on a machine that is signed in.")
	if !strings.HasPrefix(key, "abh_") || strings.ContainsAny(key, " \t") {
		return invalid
	}
	if local {
		if _, err := a.ensureLocal(ctx); err != nil {
			return err
		}
	}
	c, err := a.client(ctx, srv, key, requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	info, err := c.info(ctx)
	if err != nil {
		return err
	}
	list, err := c.keys(ctx, "")
	if e := (*Error)(nil); errors.As(err, &e) && e.Code == "unauthorized" {
		return invalid
	}
	if err != nil {
		return err
	}
	var k api.AccessKey
	for _, each := range list.Keys {
		if each.Id == list.CurrentKeyId {
			k = each
		}
	}
	replaced, err := a.saveLogin(srv, local, serverLogin{
		URL: srv.URL, ServerID: info.ServerId, PersonID: list.Person.Id, Handle: list.Person.Handle,
		KeyID: k.Id, KeyName: k.Name, Key: key,
	})
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Logged in to %s as %s with the key %q, saved for that server only.\n", srv.URL, list.Person.Handle, k.Name)
	if prev := list.CurrentKeyPreviousUse; prev != nil {
		text += a.out().warn("The key was last used "+agoText(*prev, time.Now())+", perhaps on another machine or browser.") + "\n"
	}
	text += "A key used in two places is revoked in both at once. To give this machine its own, run aboard keys create <name> and log in with that key.\n"
	a.emit(map[string]any{
		"server": srv, "server_id": info.ServerId, "person": list.Person, "key": k,
		"previous_use": list.CurrentKeyPreviousUse, "replaced": replaced,
	}, text)
	return nil
}

// parseServerURL reads a server's address. One other than this machine must use https,
// since the key would travel to it.
func parseServerURL(s string) (serverRef, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return serverRef{}, newError("invalid_request", "That isn't a server address.",
			"Give the server's address, such as https://team.example.com.")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return serverRef{}, newError("insecure_server",
			"The address reaches "+u.Host+" over http, which would send your key unencrypted.",
			"Use the server's https address.")
	}
	serverURL := u.Scheme + "://" + u.Host
	return serverRef{Name: serverURL, URL: serverURL}, nil
}

// readKey reads the key from standard input: asked for without echo at a terminal, else
// its first line.
func (a *app) readKey(srv serverRef) (string, error) {
	if a.interactive() {
		key, err := a.asker().secret("Access key for "+srv.URL, "Paste the key; it isn't shown.")
		return strings.TrimSpace(key), err
	}
	line, err := bufio.NewReader(a.env.Stdin).ReadString('\n')
	if strings.TrimSpace(line) == "" {
		if err == nil {
			err = fmt.Errorf("empty line")
		}
		return "", &Error{
			Code: "key_invalid", Message: "No key was given on standard input.",
			Hint: "Pipe the key in, such as: aboard login " + srv.URL + " < key.txt, or run aboard login in a terminal and paste it.",
			Err:  err,
		}
	}
	return strings.TrimSpace(line), nil
}

// saveLogin keeps key for srv alone, replacing what this machine had for it, and reports
// whether it had a key. For the local server it is the local owner key.
func (a *app) saveLogin(srv serverRef, local bool, login serverLogin) (bool, error) {
	p, err := a.paths()
	if err != nil {
		return false, err
	}
	if local {
		_, err := a.readOwnerToken(srv)
		had := err == nil
		return had, writeFileAtomic(p.ownerToken(), []byte(login.Key+"\n"), 0o600)
	}
	var saved serverLogins
	replaced := false
	err = updateJSONFile(p.servers(), &saved, func() error {
		for i, s := range saved.Servers {
			if s.URL == login.URL {
				saved.Servers[i], replaced = login, true
				return nil
			}
		}
		saved.Servers = append(saved.Servers, login)
		return nil
	})
	return replaced, err
}
