// Package server runs an Aboard server: it opens the database, prepares the server's
// identity and, in local mode, its owner, then serves the API until the context ends.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/rules"
	"github.com/leonidas1712/aboard/server/internal/store"
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
	// OwnerTokenPath is where local mode writes the owner's token (mode 0600) the first
	// time it starts.
	OwnerTokenPath string
	Version        string
	Log            *slog.Logger
	Clock          clock.Clock
	Rand           io.Reader
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

// Run serves a local server until ctx is done, then shuts down gracefully.
func Run(ctx context.Context, o Options) error {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	st, err := store.Open(ctx, filepath.Join(o.DataDir, "aboard.db"))
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
	svc := board.New(st, o.Clock, ids.New(o.Rand), key, board.Config{ServerID: serverID, Mode: "local", JoinHost: JoinHost(o.Addr)}, o.Log)
	token, err := svc.BootstrapOwner(ctx, rules.NormalizeName(o.OwnerName))
	if err != nil {
		return err
	}
	if token != "" {
		if err := writePrivate(o.OwnerTokenPath, token+"\n"); err != nil {
			return fmt.Errorf("save owner token: %w", err)
		}
	}
	handler, err := api.NewHandler(api.Options{Service: svc, Store: st, Clock: o.Clock, Log: o.Log, Version: o.Version, JoinsPerMinute: 30})
	if err != nil {
		return err
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.Addr, err)
	}
	pidFile := filepath.Join(o.DataDir, "server.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write pid file: %w", err)
	}
	defer func() { _ = os.Remove(pidFile) }() // best effort; a stale pid file is harmless

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Long enough for the longest inbox wait (600 s) plus a margin.
		WriteTimeout: 11 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
	o.Log.Info("serving", "addr", ln.Addr().String(), "server_id", serverID)
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
func identity(ctx context.Context, st *store.Store, o Options) (serverID string, key []byte, err error) {
	var keyHex string
	err = st.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if serverID, err = tx.Meta("server_id"); errors.Is(err, store.ErrNotFound) {
			if serverID, err = ids.New(o.Rand).ID("srv", o.Clock.Now()); err != nil {
				return err
			}
			if err := tx.SetMeta("server_id", serverID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if keyHex, err = tx.Meta("digest_key"); errors.Is(err, store.ErrNotFound) {
			b := make([]byte, 32)
			if _, err := io.ReadFull(o.Rand, b); err != nil {
				return fmt.Errorf("read randomness: %w", err)
			}
			keyHex = hex.EncodeToString(b)
			return tx.SetMeta("digest_key", keyHex)
		}
		return err
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

// writePrivate writes a file only its owner can read.
func writePrivate(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
