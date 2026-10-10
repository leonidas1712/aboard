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
		name := logins.Names[l.URL]
		if name == "" {
			name = uniqueServerName(serverHostName(l.URL), l.URL, known)
		}
		known = append(known, knownServer{Name: name, URL: l.URL, Handle: l.Handle})
	}
	for i := range known {
		if name := logins.Names[known[i].URL]; name != "" {
			known[i].Name = name
		}
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

// resolveServer uses an explicit issuer or the persisted machine default.
func (a *app) resolveServer(flag string) (serverRef, error) {
	if flag = strings.TrimSpace(flag); flag != "" {
		srv, err := a.namedServer(flag)
		if err != nil {
			return serverRef{}, err
		}
		return a.selectedServer(srv, "flag"), nil
	}
	known, def, err := a.knownServers()
	if err != nil {
		return serverRef{}, err
	}
	switch {
	case def != nil:
		return a.selectedServer(*def, "default"), nil
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
	e := newError("server_not_selected", "No default server is selected.", "Run aboard servers use NAME, or pass --server NAME|URL. With no known server, run aboard up or aboard connect <invite link>.")
	labelled := make([]map[string]string, 0, len(known))
	for _, k := range known {
		labelled = append(labelled, map[string]string{"name": k.Name, "url": k.URL, "command": "aboard servers use " + commandWord(k.Name)})
	}
	e.Details = map[string]any{"choices": choices, "server_choices": labelled}
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
// "aboard servers use <url|name>", which makes one the default.
func runServers(_ context.Context, a *app, args []string) error {
	flags := a.flags("servers")
	pos, err := a.parse(flags, args, serversUsage, 0, 3)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		if pos[0] == "use" && len(pos) == 2 {
			return runServersUse(a, pos[1])
		}
		if (pos[0] == "name" || pos[0] == "rename") && len(pos) == 3 {
			return a.runServerName(pos[1], pos[2])
		}
		return usageError("Use servers, servers use <server>, or servers name <server> <name>.", serversUsage)
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
		st := a.out()
		text = "Servers this machine knows:\n"
		rows := make([][]string, 0, len(known))
		for _, k := range known {
			mark, who, url := "", "logged in", "  "+k.URL
			switch {
			case k.Local:
				who = "local server"
			case k.Handle != "":
				who = "logged in as @" + k.Handle
			}
			if k.Default {
				mark, url = "default", "* "+k.URL
			}
			rows = append(rows, []string{k.Name, url, who, mark})
		}
		text += st.table([]string{"NAME", "SERVER", "LOGIN", "DEFAULT"}, rows, func(col int, c string, _ []string) string {
			switch col {
			case 1:
				return c[:2] + st.name(c[2:])
			case 3:
				return st.ok(c)
			}
			return c
		})
		if def == nil && len(known) > 1 {
			text += "No default server: person commands need --server. Choose one with: aboard servers use <url|name>\n"
		} else {
			text += "Change the default with: aboard servers use <url|name>\n"
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
	srv, err := a.namedServer(name)
	if err != nil {
		return err
	}
	want := srv.URL
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

// offerDefault runs after this machine signs in to srv, and decides whether srv becomes
// its default server (D203). Signing in to a team's server never moves a machine off
// what it already uses: a machine with a default keeps it, and one that already uses
// its local server keeps that as its default (knownServers). Only on a machine with no default and no local server does srv become
// the default. At a terminal, a machine whose default is another team's server is asked
// whether to switch. It returns whether srv is the default now, and the text to add to
// the command's output.
func (a *app) offerDefault(srv serverRef) (isDefault bool, text string, err error) {
	local := a.localServer()
	if srv.URL == local.URL {
		return false, "", nil
	}
	known, def, err := a.knownServers()
	if err != nil {
		return false, "", err
	}
	if def != nil && def.URL == srv.URL {
		return true, "", nil
	}
	switch {
	case def == nil:
		if err := a.setDefaultServer(srv); err != nil {
			return false, "", err
		}
		if len(known) < 2 {
			return true, "", nil
		}
		return true, "The default server is now " + srv.URL + ".\n", nil
	case def.URL != local.URL && a.interactive():
		ok, err := a.asker().confirm("Make "+srv.URL+" this machine's default server instead of "+def.URL+"?",
			"Person commands act on the default server. Change it later with aboard servers use.", false)
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
	stays := def.URL
	if def.URL == local.URL {
		stays = "the local server"
	}
	return false, "Your default stays " + stays + "; use --server " + srv.URL + " or aboard servers use " + srv.URL + " to switch.\n", nil
}

// bootstrapServer establishes the first local default only on a machine with no server.
func (a *app) bootstrapServer(ctx context.Context, flag string) (serverRef, bool, error) {
	if flag == "" {
		known, _, err := a.knownServers()
		if err != nil {
			return serverRef{}, false, err
		}
		if len(known) == 0 {
			started, err := a.ensureLocal(ctx)
			if err != nil {
				return serverRef{}, false, err
			}
			srv := a.selectedServer(a.localServer(), "local")
			if err := a.setDefaultServer(srv); err != nil {
				return serverRef{}, false, err
			}
			return srv, started, nil
		}
	}
	return a.personServer(ctx, flag)
}
