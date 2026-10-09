package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	Servers []serverLogin     `json:"servers"`
	Names   map[string]string `json:"names,omitempty"`
	// Default is this machine's default server (aboard servers use): a server's URL, or
	// "local" for the local server. Empty when none is set.
	Default string `json:"default,omitempty"`
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

// runConnect connects this machine to a server. With an invite link, the server creates
// this person with their first access key. With the server's address alone, a person
// already on the server approves this machine from one where they are signed in, and
// this machine collects a new key of theirs. Either way the key is saved for that server
// alone. It is up to a person, so it refuses inside a harness session.
func runConnect(ctx context.Context, a *app, args []string) error {
	fs := a.flags("connect")
	handleFlag := fs.String("handle", "", "your name on the server; default: your system user name")
	display := fs.String("display-name", "", "the name people see beside your handle, such as \"Maya Chen\"")
	serverName := fs.String("server-name", "", "a name for this server on this machine")
	nameFlag := fs.String("name", "", "this machine's name, which names its key; default: its host name")
	connectUsage := usageOf("connect")
	pos, err := a.parse(fs, args, connectUsage, 1, 1)
	if err != nil {
		return err
	}
	if isInviteLink(pos[0]) && a.agentSelected("") {
		if *display != "" || *serverName != "" {
			return usageError("Agent invite setup takes --handle and --name; set other account details afterwards.", connectUsage)
		}
		setupArgs := []string{pos[0]}
		if *handleFlag != "" {
			setupArgs = append(setupArgs, "--handle", *handleFlag)
		}
		if *nameFlag != "" {
			setupArgs = append(setupArgs, "--name", *nameFlag)
		}
		return runSetup(ctx, a, setupArgs)
	}
	if err := a.refuseInSession("Connecting this machine to a server", "aboard connect "+shellWord(pos[0])); err != nil {
		return err
	}
	name := *nameFlag
	if name == "" {
		name = machineName()
	}
	if !isInviteLink(pos[0]) {
		if *display != "" {
			return usageError("--display-name goes with an invite link: a machine you approve joins as the person you already are.", connectUsage)
		}
		srv, err := a.namedServer(pos[0])
		if err != nil {
			return err
		}
		if err := a.prepareServerName(&srv, *serverName); err != nil {
			return err
		}
		a.selectedServer(srv, "flag")
		if err := a.checkNotConnected(srv); err != nil {
			return err
		}
		handle := strings.TrimSpace(*handleFlag)
		if handle == "" {
			if !a.interactive() {
				return usageError("Name the person this machine is for with --handle, such as --handle "+rules.NormalizeName(a.env.Getenv("USER"))+
					": only their approval counts.", connectUsage)
			}
			if handle, err = a.asker().text("Your handle on "+hostOf(srv), "Only you can approve this machine, from one where you're signed in.", rules.NormalizeName(a.env.Getenv("USER"))); err != nil {
				return err
			}
		}
		return connectByApproval(ctx, a, srv, strings.TrimSpace(handle), name)
	}
	srv, invite, err := parseInviteLink(pos[0])
	if err != nil {
		return err
	}
	if err := a.prepareServerName(&srv, *serverName); err != nil {
		return err
	}
	a.selectedServer(srv, "flag")
	if err := a.checkNotConnected(srv); err != nil {
		return err
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
	req := api.ConnectRequest{Invite: invite, Handle: strings.TrimSpace(handle), KeyName: name, DisplayName: optional(strings.TrimSpace(*display))}
	r, err := c.api.ConnectWithResponse(ctx, nil, req)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	return a.saveConnection(srv, r.JSON201)
}

// hostOf is a server's host and port, as people name it.
func hostOf(srv serverRef) string {
	if u, err := url.Parse(srv.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return srv.URL
}

// isInviteLink reports whether s is meant as an invite link rather than a server's
// address: it names the join page or carries a fragment.
func isInviteLink(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err != nil || u.Fragment != "" || strings.TrimSuffix(u.Path, "/") == "/join"
}

// checkNotConnected refuses a server this machine already has a key for, and this
// machine's own local server, which already knows its owner.
func (a *app) checkNotConnected(srv serverRef) error {
	if srv.URL == a.localServer().URL {
		return newError("already_connected", "That is this machine's own local server, which already knows you as its owner.",
			"Run aboard connect on the machine that should join the server.")
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
	return nil
}

// saveConnection keeps the key a server gave this machine, for that server alone, and
// says who this machine is now connected as.
func (a *app) saveConnection(srv serverRef, got *api.Connected) error {
	login := serverLogin{
		URL: srv.URL, ServerID: got.ServerId, PersonID: got.Person.Id, Handle: got.Person.Handle,
		KeyID: got.Key.Id, KeyName: got.Key.Name, Key: got.Key.Token,
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	var saved serverLogins
	err = updateJSONFile(p.servers(), &saved, func() error {
		if _, ok := saved.find(srv.URL); ok {
			return newError("already_connected", "This machine connected to "+srv.URL+" meanwhile.", "Use that connection.")
		}
		if err := a.saveServerName(&saved, srv); err != nil {
			return err
		}
		saved.Servers = append(saved.Servers, login)
		return nil
	})
	if err != nil {
		return err
	}
	a.selectedServer(srv, "flag")
	key := got.Key
	isDefault, extra, err := a.offerDefault(srv)
	if err != nil {
		return err
	}
	a.emit(map[string]any{
		"server": srv, "server_id": got.ServerId, "person": got.Person,
		"key":     map[string]any{"id": key.Id, "name": key.Name, "created_at": key.CreatedAt, "expires_at": key.ExpiresAt},
		"default": isDefault,
	}, fmt.Sprintf("Connected to %s as %s (%s). This machine's key, %q, is saved.\n", srv.URL, got.Person.Handle, got.Person.ServerRole, key.Name)+extra)
	return nil
}

// approvalPollBounds keep a waiting machine's polling within reason whatever the server
// asks: between one and ten seconds apart, and never past the request's expiry.
const (
	minApprovalPoll = time.Second
	maxApprovalPoll = 10 * time.Second
	// maxApprovalWait bounds the wait however late the server says the request expires.
	maxApprovalWait = 15 * time.Minute
)

// connectByApproval asks srv for a key for this machine, shows the short code a person
// approves from a machine where they are signed in, and waits, polling with the secret
// only this machine holds, until the request is approved (and the key collected),
// refused or expired.
func connectByApproval(ctx context.Context, a *app, srv serverRef, handle, name string) error {
	c, err := a.newClient(srv, "", requestTimeout)
	if err != nil {
		return err
	}
	r, err := c.api.StartMachineRequestWithResponse(ctx, nil, api.StartMachineRequest{Handle: handle, Label: name})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON201 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	started := r.JSON201
	waitFor := time.Until(started.ExpiresAt)
	if waitFor > maxApprovalWait {
		waitFor = maxApprovalWait
	}
	// The instructions go to standard output in text, and to standard error with --json,
	// whose standard output holds only the result.
	out := a.env.Stdout
	if a.json {
		out = a.env.Stderr
	}
	_, _ = fmt.Fprintf(out, "Connecting this machine (%q) to %s as %s.\nOn a machine where @%s is signed in, run: aboard approve %s --server %s\nThe code expires in %s. Or paste a key with: aboard login %s\n",
		name, srv.URL, handle, handle, started.Code, srv.URL, durationText(waitFor), srv.URL)

	deadline := time.Now().Add(waitFor)
	every := pollEvery(started.PollIntervalSeconds)
	for attempt := 0; time.Now().Before(deadline) && attempt < int(maxApprovalWait/minApprovalPoll); attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
		got, err := c.api.CollectMachineRequestWithResponse(ctx, nil, api.CollectMachineRequest{Secret: started.Secret})
		if err != nil {
			return c.unreachable(err)
		}
		switch {
		case got.JSON201 != nil:
			return a.saveConnection(srv, got.JSON201)
		case got.JSON202 != nil:
			every = pollEvery(got.JSON202.PollIntervalSeconds)
		case got.StatusCode() == http.StatusTooManyRequests:
			every = maxApprovalPoll
		default:
			return apiError(got.StatusCode(), got.Body)
		}
	}
	return newError("machine_request_invalid", "Nobody approved this machine before its code expired.",
		"Run aboard connect "+srv.URL+" again for a new code, or paste a key with aboard login "+srv.URL+".")
}

// pollEvery is how long to wait between collection attempts, as the server asks, within
// the client's own bounds.
func pollEvery(seconds int) time.Duration {
	d := time.Duration(seconds) * time.Second
	return min(max(d, minApprovalPoll), maxApprovalPoll)
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
