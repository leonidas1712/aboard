// Package server runs an Aboard server: it opens the database, prepares the server's
// identity and, in local mode, its owner, then serves the API until the context ends.
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/rules"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
	"github.com/leonidas1712/aboard/web"
)

// DefaultLocalAddr is where the local server listens unless told otherwise.
const DefaultLocalAddr = "127.0.0.1:7400"

// Options configures a server.
type Options struct {
	// Addr is the address to listen on, such as 127.0.0.1:7400.
	Addr string
	// DataDir holds aboard.db, server.pid and server.log.
	DataDir string
	// OwnerName is the local owner's login name, normalized into a member name.
	OwnerName string
	// MachineName names the owner's first access key, normalized like a member name.
	MachineName string
	// OwnerTokenPath is where local mode writes the owner's token (mode 0600) the first
	// time it starts.
	OwnerTokenPath string
	Version        string
	// Commit and CommitTime name the Git commit the server was built from, when known.
	Commit     string
	CommitTime time.Time
	Log        *slog.Logger
	Clock      clock.Clock
	Rand       io.Reader
	// Team, when set, runs a team server instead of the local one: OwnerName,
	// MachineName and OwnerTokenPath are then unused.
	Team *Team
}

// Team configures a team server, which runs behind a proxy that ends HTTPS.
type Team struct {
	// PublicURL is the address people use, as ParsePublicURL returns it.
	PublicURL PublicURL
	// AdminName is the first admin's handle, made on the first start of an empty
	// database.
	AdminName string
}

// PublicURL is a team server's public address.
type PublicURL struct {
	// Origin is the URL's scheme, host and port: https://team.example.com.
	Origin string
	// Host is the Host header requests arrive with, with the port when it isn't 443.
	Host string
}

// AdminKeyFile is the file in a team server's data folder that the first admin's key is
// written to.
const AdminKeyFile = "admin-key"

// ParsePublicURL checks a team server's public URL: https, a host, a port from 1 to
// 65535 if any, and no user, path (not even /), query or fragment. The default port is dropped, as browsers drop it from Origin and
// Host.
func ParsePublicURL(s string) (PublicURL, error) {
	bad := fmt.Errorf("%q is not an https address such as https://team.example.com, with no path, not even /, and a port from 1 to 65535 if any", s)
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return PublicURL{}, bad
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return PublicURL{}, bad
		}
	}
	host := strings.ToLower(u.Host)
	if u.Port() == "443" {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return PublicURL{Origin: "https://" + host, Host: host}, nil
}

// JoinHost is how join lines name a local server at addr: "localhost", with the port
// when it isn't the default.
func JoinHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	_, defPort, _ := net.SplitHostPort(DefaultLocalAddr)
	if err != nil || port == defPort {
		return "localhost"
	}
	return "localhost:" + port
}

// Run serves a local server, or a team server with o.Team, until ctx is done, then shuts
// down gracefully.
func Run(ctx context.Context, o Options) error {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	if o.Team != nil {
		if err := prepareTeamData(o.DataDir); err != nil {
			return err
		}
	} else if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	st, err := sqlite.Open(ctx, filepath.Join(o.DataDir, "aboard.db"), o.Clock)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			o.Log.Error("close database", "error", err)
		}
	}()

	serverID, key, err := identity(ctx, st, o)
	if err != nil {
		return err
	}
	cfg := board.Config{ServerID: serverID, Mode: "local", JoinHost: JoinHost(o.Addr)}
	if o.Team != nil {
		cfg.Mode, cfg.JoinHost = "team", o.Team.PublicURL.Host
	}
	svc := board.New(st, notify.NewInProcess(), o.Clock, ids.New(o.Rand), key, cfg, o.Log)
	if o.Team != nil {
		if err := firstAdmin(ctx, svc, o.DataDir, o.Team.AdminName, o.Log); err != nil {
			return err
		}
	} else {
		token, err := svc.BootstrapOwner(ctx, rules.NormalizeName(o.OwnerName), rules.NormalizeName(o.MachineName))
		if err != nil {
			return err
		}
		if token != "" {
			if err := writePrivate(o.OwnerTokenPath, token+"\n"); err != nil {
				return fmt.Errorf("save owner token: %w", err)
			}
		}
	}
	// shutdown ends open event streams when the server shuts down: http.Server.Shutdown
	// waits for active requests, and a stream never finishes on its own.
	shutdown, startShutdown := context.WithCancel(context.WithoutCancel(ctx))
	defer startShutdown()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.Addr, err)
	}
	hosts, publicOrigin := api.LocalHosts(ln.Addr().String()), ""
	if o.Team != nil {
		hosts, publicOrigin = []string{o.Team.PublicURL.Host}, o.Team.PublicURL.Origin
	}
	handler, err := api.NewHandler(api.Options{
		Service: svc, Responses: st, Clock: o.Clock, Log: o.Log, Version: o.Version, Commit: o.Commit, CommitTime: o.CommitTime, JoinsPerMinute: 30, ConnectsPerMinute: 10, ConnectsPerMinuteServer: 60,
		MachineRequests: api.Limits{PerAddr: 10, Server: 60}, MachineCodes: api.Limits{PerAddr: 10, PerPerson: 10, Server: 60},
		MachineCollects: api.Limits{PerAddr: 60, Server: 600},
		SignInFailures:  api.Limits{PerAddr: 20, Server: 100}, SignInAttempts: api.Limits{PerAddr: 120, Server: 600},
		Shutdown: shutdown, Hosts: hosts, PublicOrigin: publicOrigin, UI: web.Files(),
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	pidFile := filepath.Join(o.DataDir, "server.pid")
	pid := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if err := os.WriteFile(pidFile, pid, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write pid file: %w", err)
	}
	// Best effort; a stale pid file is harmless. Only our own: once the listener closes,
	// aboard up may start a new server, which writes its pid before this one exits.
	defer func() {
		if b, err := os.ReadFile(pidFile); err == nil && bytes.Equal(b, pid) { //nolint:gosec // our own data directory
			_ = os.Remove(pidFile)
		}
	}()

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Long enough for the longest inbox wait (600 s) plus a margin. The event stream
		// on GET /v1/stream moves its own write deadline forward as it writes.
		WriteTimeout: 11 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
	srv.RegisterOnShutdown(startShutdown)
	o.Log.Info("serving", "addr", ln.Addr().String(), "server_id", serverID, "mode", cfg.Mode)
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	})
	return g.Wait()
}

// identity returns the server's id and digest key, creating them on first start.
// settings stores the server's own configuration, such as its id and digest key.
type settings interface {
	Setting(ctx context.Context, key string) (value string, ok bool, err error)
	SettingOnce(ctx context.Context, key, value string) (stored string, err error)
}

func identity(ctx context.Context, st settings, o Options) (serverID string, key []byte, err error) {
	serverID, err = setting(ctx, st, "server_id", func() (string, error) {
		return ids.New(o.Rand).ID("srv", o.Clock.Now())
	})
	if err != nil {
		return "", nil, fmt.Errorf("load server identity: %w", err)
	}
	keyHex, err := setting(ctx, st, "digest_key", func() (string, error) {
		b := make([]byte, 32)
		if _, err := io.ReadFull(o.Rand, b); err != nil {
			return "", fmt.Errorf("read randomness: %w", err)
		}
		return hex.EncodeToString(b), nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("load server identity: %w", err)
	}
	key, err = hex.DecodeString(keyHex)
	if err != nil {
		return "", nil, fmt.Errorf("server digest key: %w", err)
	}
	return serverID, key, nil
}

// setting returns a stored server setting, first storing the value from create if
// there is none. When another start stored one in between, that one is kept and returned.
func setting(ctx context.Context, st settings, key string, create func() (string, error)) (string, error) {
	v, ok, err := st.Setting(ctx, key)
	if err != nil || ok {
		return v, err
	}
	if v, err = create(); err != nil {
		return "", err
	}
	return st.SettingOnce(ctx, key, v)
}

// writePrivate writes a file only its owner can read.
func writePrivate(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
