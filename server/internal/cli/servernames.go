package cli

import (
	"fmt"
	"net/url"
	"strings"
)

func validServerName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for i, r := range name {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		digit := r >= '0' && r <= '9'
		if !letter && (i == 0 || !digit && r != '-' && r != '_') {
			return false
		}
	}
	return true
}

func serverHostName(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return "server"
	}
	name := strings.Split(u.Hostname(), ".")[0]
	if !validServerName(name) {
		return "server"
	}
	return name
}

func uniqueServerName(want, address string, known []knownServer) string {
	for n := 1; ; n++ {
		candidate := want
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			base := want
			if len(base)+len(suffix) > 63 {
				base = base[:63-len(suffix)]
			}
			candidate = base + suffix
		}
		used := strings.EqualFold(candidate, localServerName)
		for _, k := range known {
			if k.URL != address && strings.EqualFold(k.Name, candidate) {
				used = true
			}
		}
		if !used {
			return candidate
		}
	}
}

func (a *app) namedServer(value string) (serverRef, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	if value == localServerName {
		return a.serverRefFor(a.localServer().URL), nil
	}
	if strings.Contains(value, "://") {
		srv, err := parseServerURL(value)
		if err != nil {
			return serverRef{}, err
		}
		return a.serverRefFor(srv.URL), nil
	}
	known, _, err := a.knownServers()
	if err != nil {
		return serverRef{}, err
	}
	for _, k := range known {
		if strings.EqualFold(k.Name, value) {
			return k.ref(), nil
		}
	}
	return serverRef{}, newError("server_unknown", "This machine doesn't know the server "+value+".", "Run aboard servers to see its names and URLs.")
}

func checkServerName(name, address, local string, known []knownServer) error {
	if !validServerName(name) {
		return newError("invalid_server_name", "That is not a valid server name.", "Use up to 63 letters, digits, hyphens or underscores, starting with a letter.")
	}
	if strings.EqualFold(name, localServerName) && address != local {
		return newError("server_name_taken", "The name local belongs to this machine's local server.", "Choose another server name.")
	}
	for _, k := range known {
		if k.URL != address && strings.EqualFold(k.Name, name) {
			return newError("server_name_taken", "The server name "+name+" is already in use.", "Choose another name, or rename that server first.")
		}
	}
	return nil
}

func (a *app) runServerName(target, name string) error {
	srv, err := a.namedServer(target)
	if err != nil {
		return err
	}
	known, _, err := a.knownServers()
	if err != nil {
		return err
	}
	found := false
	for _, k := range known {
		if k.URL == srv.URL {
			found = true
		}
	}
	if !found {
		return newError("server_unknown", "This machine doesn't know the server "+target+".", "Connect or log in to it first.")
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	var saved serverLogins
	err = updateJSONFile(p.servers(), &saved, func() error {
		// Check the labels again under the file lock; concurrent renames must not collide.
		current := append([]knownServer(nil), known...)
		for i := range current {
			if n := saved.Names[current[i].URL]; n != "" {
				current[i].Name = n
			}
		}
		for address, n := range saved.Names {
			current = append(current, knownServer{Name: n, URL: address})
		}
		if err := checkServerName(name, srv.URL, a.localServer().URL, current); err != nil {
			return err
		}
		if saved.Names == nil {
			saved.Names = map[string]string{}
		}
		saved.Names[srv.URL] = name
		return nil
	})
	if err != nil {
		return err
	}
	previous := srv.Name
	srv.Name = name
	a.emit(map[string]any{"server": srv, "previous_name": previous}, fmt.Sprintf("Named %s %s on this machine.\n", srv.URL, name))
	return nil
}

type serverSelection struct {
	Server serverRef `json:"server"`
	Source string    `json:"source"`
}

func (a *app) selectedServer(srv serverRef, source string) serverRef {
	srv = a.serverRefFor(srv.URL)
	if !a.actsForAgent() {
		a.serverSelection = &serverSelection{Server: srv, Source: source}
	}
	return srv
}

func (a *app) prepareServerName(srv *serverRef, name string) error {
	known, _, err := a.knownServers()
	if err != nil {
		return err
	}
	if name == "" {
		for _, k := range known {
			if k.URL == srv.URL {
				name = k.Name
				break
			}
		}
		if name == "" {
			name = uniqueServerName(serverHostName(srv.URL), srv.URL, known)
		}
	}
	if err := checkServerName(name, srv.URL, a.localServer().URL, known); err != nil {
		return err
	}
	srv.Name = name
	return nil
}

func (a *app) saveServerName(saved *serverLogins, srv serverRef) error {
	known := []knownServer{{Name: localServerName, URL: a.localServer().URL}}
	for _, login := range saved.Servers {
		name := saved.Names[login.URL]
		if name == "" {
			name = uniqueServerName(serverHostName(login.URL), login.URL, known)
		}
		known = append(known, knownServer{Name: name, URL: login.URL})
	}
	for address, name := range saved.Names {
		known = append(known, knownServer{Name: name, URL: address})
	}
	if err := checkServerName(srv.Name, srv.URL, a.localServer().URL, known); err != nil {
		return err
	}
	if saved.Names == nil {
		saved.Names = map[string]string{}
	}
	saved.Names[srv.URL] = srv.Name
	return nil
}

// explainServer keeps single-server commands quiet unless the folder changes their target.
func (a *app) explainServer(selection *serverSelection) bool {
	known, def, err := a.knownServers()
	if err != nil {
		return false
	}
	if len(known) > 1 {
		return true
	}
	if selection.Source != "project" {
		return false
	}
	normal := a.localServer().URL
	if def != nil {
		normal = def.URL
	} else if len(known) == 1 {
		normal = known[0].URL
	}
	return selection.Server.URL != normal
}
