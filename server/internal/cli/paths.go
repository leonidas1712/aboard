package cli

import (
	"errors"
	"net"
	"path/filepath"

	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/server"
)

// paths are the directories where aboard keeps its files on this machine.
type paths struct {
	// config holds the local owner token and agent credentials.
	config string
	// data holds the local server's database, pid file and log.
	data string
	// state holds the audit heads this machine has verified and the delivery daemon's
	// lock, pid, log, journal and socket.
	state string
}

func (p paths) ownerToken() string  { return filepath.Join(p.config, "local-owner-token") }
func (p paths) credentials() string { return filepath.Join(p.config, "credentials.json") }
func (p paths) heads() string       { return filepath.Join(p.state, "heads.json") }
func (p paths) pidFile() string     { return filepath.Join(p.data, "server.pid") }
func (p paths) serverLog() string   { return filepath.Join(p.data, "server.log") }

// The delivery daemon's files live in the state directory.
func (p paths) daemonLock() string { return filepath.Join(p.state, "daemon.lock") }
func (p paths) daemonPID() string  { return filepath.Join(p.state, "daemon.pid") }
func (p paths) daemonLog() string  { return filepath.Join(p.state, "daemon.log") }
func (p paths) deliveryDB() string { return filepath.Join(p.state, "delivery.db") }
func (p paths) socket() string     { return control.Path(p.state) }

// resolvePaths follows the XDG base directory variables, falling back to the usual
// directories under $HOME.
func resolvePaths(getenv func(string) string) (paths, error) {
	home := getenv("HOME")
	dir := func(xdgVar string, fallback ...string) (string, error) {
		if v := getenv(xdgVar); filepath.IsAbs(v) {
			return filepath.Join(v, "aboard"), nil
		}
		if home == "" {
			return "", errors.New("HOME is not set")
		}
		return filepath.Join(append(append([]string{home}, fallback...), "aboard")...), nil
	}
	var p paths
	var err error
	if p.config, err = dir("XDG_CONFIG_HOME", ".config"); err != nil {
		return paths{}, homeError(err)
	}
	if p.data, err = dir("XDG_DATA_HOME", ".local", "share"); err != nil {
		return paths{}, homeError(err)
	}
	if p.state, err = dir("XDG_STATE_HOME", ".local", "state"); err != nil {
		return paths{}, homeError(err)
	}
	return p, nil
}

func homeError(err error) *Error {
	return &Error{
		Code: "invalid_request", Message: "Can't tell where to keep aboard's files because HOME is not set.",
		Hint: "Set HOME, or set XDG_CONFIG_HOME, XDG_DATA_HOME and XDG_STATE_HOME.", Err: err,
	}
}

// paths returns this machine's aboard directories.
func (a *app) paths() (paths, error) { return resolvePaths(a.env.Getenv) }

// localAddr is the address the local server listens on.
func (a *app) localAddr() string {
	if v := a.env.Getenv("ABOARD_LOCAL_ADDR"); v != "" {
		return v
	}
	return server.DefaultLocalAddr
}

// localServerName is how the local server is named in output and in .aboard.
const localServerName = "local"

// serverRef names a server in output and in the project file.
type serverRef struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// localServer is the server on this machine.
func (a *app) localServer() serverRef {
	return serverRef{Name: localServerName, URL: "http://" + a.localAddr()}
}

// mapJoinServer decides which server a join line's host names. Only the local server
// is known: "localhost" (the default port), or localhost or 127.0.0.1 with the local
// server's port.
func mapJoinServer(host, localAddr string) (serverRef, error) {
	_, localPort, err := net.SplitHostPort(localAddr)
	if err != nil {
		return serverRef{}, newError("invalid_request",
			"The local server address "+localAddr+" is not a host and port.",
			"Set ABOARD_LOCAL_ADDR to an address such as 127.0.0.1:7400, or unset it.")
	}
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h = host
		_, port, _ = net.SplitHostPort(server.DefaultLocalAddr)
	}
	if (h == "localhost" || h == "127.0.0.1") && port == localPort {
		return serverRef{Name: localServerName, URL: "http://" + localAddr}, nil
	}
	return serverRef{}, newError("server_unknown",
		"The join line names the server "+host+", which this machine doesn't know.",
		"Only the local Aboard server on this machine can be joined. Ask for a join line for this machine's local server.")
}
