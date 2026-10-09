package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func remoteOrigin(o options) (string, error) {
	if o.Server == "" && o.AdminKeyFile == "" {
		return "", nil
	}
	if o.Server == "" || o.AdminKeyFile == "" {
		return "", errors.New("remote load requires both --server and --admin-key-file")
	}
	u, err := url.Parse(o.Server)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("--server must be an HTTPS origin without user info, path, query or fragment")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("--server has an invalid port")
		}
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return "", errors.New("--server requires HTTPS, except on loopback")
	}
	return u.String(), nil
}

func loadClient() *http.Client { return loadClientWithDial((&net.Dialer{}).DialContext) }
func loadClientWithDial(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	return &http.Client{Timeout: 65 * time.Second, Transport: loadTransport(dial, 16), CheckRedirect: refuseLoadRedirect}
}

func refuseLoadRedirect(_ *http.Request, _ []*http.Request) error {
	return errors.New("load refuses HTTP redirects")
}

func (f *fixture) setPhase(phase string) {
	if f.monitor != nil {
		f.monitor.setPhase(phase)
	}
}

func (f *fixture) setupServer(o options) error {
	origin, err := remoteOrigin(o)
	if err != nil {
		return err
	}
	if origin == "" {
		return f.setupLocalServer()
	}
	info, err := os.Lstat(o.AdminKeyFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("--admin-key-file must be a private regular file (0600)")
	}
	raw, err := os.ReadFile(o.AdminKeyFile)
	if err != nil {
		return errors.New("could not read remote admin key file")
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return errors.New("remote admin key file is empty")
	}
	f.url = origin
	f.admin, err = f.machine("admin", "127.0.0.1:1")
	if err != nil {
		return err
	}
	f.admin.key = key
	if _, err := f.api(f.ctx, http.MethodGet, "/v1/me", key, nil); err != nil {
		return err
	}
	return f.cli(f.admin, key+"\n", "login", origin, "--json")
}
