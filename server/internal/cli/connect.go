package cli

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// serverLogin is this machine's access key for a server it connected to with an invite.
// The key is sent only to the server at URL, which issued it.
type serverLogin struct {
	URL      string `json:"url"`
	ServerID string `json:"server_id"`
	PersonID string `json:"person_id"`
	Handle   string `json:"handle"`
	KeyID    string `json:"key_id"`
	KeyName  string `json:"key_name"`
	Key      string `json:"key"`
}

// serverLogins is the content of servers.json: one login per server.
type serverLogins struct {
	Servers []serverLogin `json:"servers"`
}

func (l serverLogins) find(serverURL string) (serverLogin, bool) {
	for _, s := range l.Servers {
		if s.URL == serverURL {
			return s, true
		}
	}
	return serverLogin{}, false
}

func (a *app) readServerLogins() (serverLogins, error) {
	p, err := a.paths()
	if err != nil {
		return serverLogins{}, err
	}
	var l serverLogins
	_, err = readJSONFile(p.servers(), &l)
	return l, err
}

// connectUsage is the usage line of aboard connect.
const connectUsage = "aboard connect <invite link> [--handle NAME] [--display-name TEXT] [--json]"

// runConnect redeems a server invite: the server creates this person with their first
// access key, which is saved for that server alone. It is up to a person, so it refuses
// inside a harness session.
func runConnect(ctx context.Context, a *app, args []string) error {
	fs := a.flags("connect")
	handleFlag := fs.String("handle", "", "your name on the server; default: your system user name")
	display := fs.String("display-name", "", "the name people see beside your handle, such as \"Maya Chen\"")
	pos, err := a.parse(fs, args, connectUsage, 1, 1)
	if err != nil {
		return err
	}
	if err := a.refuseInSession("Connecting this machine to a server", "aboard connect <invite link>"); err != nil {
		return err
	}
	srv, invite, err := parseInviteLink(pos[0])
	if err != nil {
		return err
	}
	if srv.URL == a.localServer().URL {
		return newError("already_connected", "The invite is for this machine's own local server, which already knows you as its owner.",
			"Run aboard connect on the machine of the person you invite.")
	}
	logins, err := a.readServerLogins()
	if err != nil {
		return err
	}
	if have, ok := logins.find(srv.URL); ok {
		e := newError("already_connected", "This machine is already connected to "+srv.URL+" as "+have.Handle+".",
			"Use that connection; a machine keeps one key per server.")
		e.Details = map[string]any{"handle": have.Handle}
		return e
	}
	handle := *handleFlag
	if handle == "" {
		handle = rules.NormalizeName(a.env.Getenv("USER"))
		if a.interactive() {
			if handle, err = a.asker().text("Your name on "+srv.URL, "Others add and mention you by it.", handle); err != nil {
				return err
			}
		}
	}
	c, err := a.newClient(srv, "", requestTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req := api.ConnectRequest{Invite: invite, Handle: strings.TrimSpace(handle), KeyName: machineName(), DisplayName: optional(strings.TrimSpace(*display))}
	r, err := c.api.ConnectWithResponse(ctx, nil, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	got := r.JSON201
	login := serverLogin{
		URL: srv.URL, ServerID: got.ServerId, PersonID: got.Person.Id, Handle: got.Person.Handle,
		KeyID: got.Key.Id, KeyName: got.Key.Name, Key: got.Key.Token,
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	var saved serverLogins
	err = updateJSONFile(p.servers(), &saved, 0o600, func() error {
		if _, ok := saved.find(srv.URL); ok {
			return newError("already_connected", "This machine connected to "+srv.URL+" meanwhile.", "Use that connection.")
		}
		saved.Servers = append(saved.Servers, login)
		return nil
	})
	if err != nil {
		return err
	}
	key := got.Key
	a.emit(map[string]any{
		"server": srv, "server_id": got.ServerId, "person": got.Person,
		"key": map[string]any{"id": key.Id, "name": key.Name, "created_at": key.CreatedAt, "expires_at": key.ExpiresAt},
	}, fmt.Sprintf("Connected to %s as %s (%s). This machine's key, %q, is saved.\n", srv.URL, got.Person.Handle, got.Person.ServerRole, key.Name))
	return nil
}

// parseInviteLink splits an invite link, <server URL>/join#abi_…, into the server and the
// invite. A server other than this machine must be reached over https.
func parseInviteLink(link string) (serverRef, string, error) {
	bad := newError("invite_link_invalid", "That isn't an invite link.",
		"Paste the whole link the admin gave you, such as https://team.example.com/join#abi_….")
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		strings.TrimSuffix(u.Path, "/") != "/join" || u.RawQuery != "" || !strings.HasPrefix(u.Fragment, "abi_") {
		return serverRef{}, "", bad
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return serverRef{}, "", newError("insecure_server",
			"The invite link reaches "+u.Host+" over http, which would send your new key unencrypted.",
			"Ask the admin for the server's https link.")
	}
	serverURL := u.Scheme + "://" + u.Host
	return serverRef{Name: serverURL, URL: serverURL}, u.Fragment, nil
}

// loopback reports whether host names this machine.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
