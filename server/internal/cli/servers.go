package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// serversUsage is the usage of "aboard servers".
var serversUsage = usageOf("servers")

// knownServer is a server this machine knows: its own local server, once that has an
// owner login, or one in servers.json.
type knownServer struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Local   bool   `json:"local"`
	Default bool   `json:"default"`
	Handle  string `json:"handle,omitempty"`
}

func (k knownServer) ref() serverRef { return serverRef{Name: k.Name, URL: k.URL} }

// knownServers lists the servers this machine knows, the local one first, and the
// default server among them, if one is set and still known.
func (a *app) knownServers() ([]knownServer, *serverRef, error) {
	p, err := a.paths()
	if err != nil {
		return nil, nil, err
	}
	logins, err := a.readServerLogins()
	if err != nil {
		return nil, nil, err
	}
	var known []knownServer
	local := a.localServer()
	if _, err := os.Stat(p.ownerToken()); err == nil {
		known = append(known, knownServer{Name: local.Name, URL: local.URL, Local: true})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	for _, l := range logins.Servers {
		if l.URL == local.URL {
			continue
		}
		known = append(known, knownServer{Name: l.URL, URL: l.URL, Handle: l.Handle})
	}
	want := logins.Default
	if want == localServerName {
		want = local.URL
	}
	var def *serverRef
	for i := range known {
		if want != "" && known[i].URL == want {
			known[i].Default = true
			ref := known[i].ref()
			def = &ref
		}
	}
	return known, def, nil
}

// resolveServer is the server a person command acts on (D202): flag, else the server
// this directory's .aboard names, else this machine's default server, else the only
// server it knows, else the local server. With several and no default it refuses with
// server_not_selected, naming --server and aboard servers use. It starts nothing.
func (a *app) resolveServer(flag string) (serverRef, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
		return a.serverRefFor(strings.TrimSuffix(flag, "/")), nil
	}
	if p, ok, err := a.readProject(); err != nil {
		return serverRef{}, err
	} else if ok && p.Server.URL != "" {
		return p.Server, nil
	}
	known, def, err := a.knownServers()
	if err != nil {
		return serverRef{}, err
	}
	switch {
	case def != nil:
		return *def, nil
	case len(known) == 0:
		return a.localServer(), nil
	case len(known) == 1:
		return known[0].ref(), nil
	}
	return serverRef{}, serverNotSelected(known)
}

// serverNotSelected is the error for a person command that can't tell which of
// several servers it is for.
func serverNotSelected(known []knownServer) *Error {
	choices := make([]string, 0, len(known))
	for _, k := range known {
		choices = append(choices, k.URL)
	}
	e := newError("server_not_selected",
		"This machine knows several servers ("+strings.Join(choices, ", ")+"), none of them its default, and nothing here chooses one.",
		"Pass --server, such as --server "+choices[len(choices)-1]+", or make one the default with aboard servers use <url|local>.")
	e.Details = map[string]any{"choices": choices}
	return e
}

// personServer is the server a person command acts on, as resolveServer picks it,
// starting the local server if that is the one. It reports whether it started it.
func (a *app) personServer(ctx context.Context, flag string) (serverRef, bool, error) {
	srv, err := a.resolveServer(flag)
	if err != nil || srv.URL != a.localServer().URL {
		return srv, false, err
	}
	started, err := a.ensureLocal(ctx)
	return srv, started, err
}

// runServers runs "aboard servers", which lists the servers this machine knows, and
// "aboard servers use <url|local>", which makes one the default.
func runServers(_ context.Context, a *app, args []string) error {
	flags := a.flags("servers")
	pos, err := a.parse(flags, args, serversUsage, 0, 2)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		if pos[0] != "use" || len(pos) != 2 {
			return usageError("aboard servers takes nothing, or use and one server: aboard servers use https://team.example.com.", serversUsage)
		}
		return runServersUse(a, pos[1])
	}
	if err := a.refuseInSession("Listing this machine's servers", "aboard servers"); err != nil {
		return err
	}
	known, def, err := a.knownServers()
	if err != nil {
		return err
	}
	if known == nil {
		known = []knownServer{}
	}
	var text string
	switch {
	case len(known) == 0:
		text = "This machine knows no server yet. Start the local one with aboard up, or join a team's with aboard connect <invite link>.\n"
	default:
		text = "Servers this machine knows:\n"
		width := 0
		for _, k := range known {
			width = max(width, len(k.URL))
		}
		for _, k := range known {
			mark, who := "  ", k.Handle
			if k.Local {
				who = "local"
			}
			if k.Default {
				mark, who = "* ", who+"  (default)"
			}
			text += strings.TrimRight(mark+k.URL+strings.Repeat(" ", width-len(k.URL)+2)+who, " ") + "\n"
		}
		if def == nil && len(known) > 1 {
			text += "No default server: outside a linked folder, person commands need --server. Choose one with: aboard servers use <url|local>\n"
		} else {
			text += "Change the default with: aboard servers use <url|local>\n"
		}
	}
	a.emit(map[string]any{"servers": known, "default": def}, text)
	return nil
}

// runServersUse makes a server this machine knows its default server.
func runServersUse(a *app, name string) error {
	if err := a.refuseInSession("Choosing this machine's default server", "aboard servers use "+commandWord(name)); err != nil {
		return err
	}
	known, before, err := a.knownServers()
	if err != nil {
		return err
	}
	want := strings.TrimSuffix(strings.TrimSpace(name), "/")
	if want == localServerName {
		want = a.localServer().URL
	}
	var chosen *knownServer
	for i := range known {
		if known[i].URL == want {
			chosen = &known[i]
		}
	}
	if chosen == nil {
		choices := make([]string, 0, len(known))
		for _, k := range known {
			choices = append(choices, k.URL)
		}
		hint := "This machine knows no server yet: start the local one with aboard up, or connect to a team's with aboard connect <invite link>."
		if len(choices) > 0 {
			hint = "Use one of " + strings.Join(choices, ", ") + ", or connect to it first with aboard connect or aboard login."
		}
		e := newError("server_unknown", "This machine doesn't know the server "+name+".", hint)
		e.Details = map[string]any{"choices": choices}
		return e
	}
	if err := a.setDefaultServer(chosen.ref()); err != nil {
		return err
	}
	a.emit(map[string]any{"server": chosen.ref(), "previous": before},
		fmt.Sprintf("The default server is now %s: person commands outside a linked folder act on it.\n", chosen.URL))
	return nil
}

// setDefaultServer saves srv as this machine's default server.
func (a *app) setDefaultServer(srv serverRef) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	value := srv.URL
	if srv.URL == a.localServer().URL {
		value = localServerName
	}
	var saved serverLogins
	return updateJSONFile(p.servers(), &saved, func() error {
		saved.Default = value
		return nil
	})
}

// offerDefault runs after this machine signs in to srv. When srv is a server other than
// the local one, the machine knows other servers too and srv isn't its default, it asks
// at a terminal whether to make srv the default, and elsewhere says how. It returns
// whether srv is the default now, and the text to add to the command's output.
func (a *app) offerDefault(srv serverRef) (isDefault bool, text string, err error) {
	known, def, err := a.knownServers()
	if err != nil {
		return false, "", err
	}
	if def != nil && def.URL == srv.URL {
		return true, "", nil
	}
	if len(known) < 2 || srv.URL == a.localServer().URL {
		return false, "", nil
	}
	if a.interactive() {
		ok, err := a.asker().confirm("Make "+srv.URL+" this machine's default server?",
			"Person commands outside a linked folder act on the default server. Change it later with aboard servers use.", true)
		if errors.Is(err, errAborted) {
			ok, err = false, nil
		}
		if err != nil {
			return false, "", err
		}
		if ok {
			if err := a.setDefaultServer(srv); err != nil {
				return false, "", err
			}
			return true, "The default server is now " + srv.URL + ".\n", nil
		}
	}
	return false, "To act on " + srv.URL + " outside a linked folder, pass --server " + srv.URL + " or run: aboard servers use " + srv.URL + "\n", nil
}
