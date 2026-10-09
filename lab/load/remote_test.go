package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteLoadUsesExistingServerWithoutOwningIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "aboard")
	// #nosec G204 -- builds a fixed repository command in a test-owned directory.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../server/cmd/aboard")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v (%d bytes)", err, len(output))
	}
	f := &fixture{root: t.TempDir(), binary: binary, ctx: ctx, client: loadClient()}
	defer f.close()
	if err := f.setup(options{}); err != nil {
		t.Fatal(err)
	}
	defer f.monitor.stop(ctx)
	key := filepath.Join(t.TempDir(), "admin-key")
	if err := os.WriteFile(key, []byte(f.admin.key), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := run(ctx, options{Binary: binary, Server: f.url, AdminKeyFile: key, People: 1, Agents: 2, Boards: 2, Rounds: 1, Writers: 2, WritesPerWriter: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" || result.Deliveries != 2 || result.VerifiedChains != 2 || result.Concurrent.Verified != 2 || result.Resources.Available {
		t.Fatalf("incomplete remote proof: %+v", result)
	}
	if _, err := f.api(ctx, "GET", "/v1/me", f.admin.key, nil); err != nil {
		t.Fatalf("remote server stopped: %v", err)
	}
}

func TestRemoteLoadRejectsOriginBeforeReadingCredentials(t *testing.T) {
	for _, target := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com?key=secret", "https://example.com#fragment"} {
		t.Run(target, func(t *testing.T) {
			_, err := remoteOrigin(options{Server: target, AdminKeyFile: "missing"})
			if err == nil {
				t.Fatal("unsafe origin accepted")
			}
		})
	}
}

func TestRemoteLoadNeverFollowsAuthenticatedRedirects(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached.Store(true) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	f := &fixture{url: origin.URL, client: loadClient()}
	defer f.client.CloseIdleConnections()
	if _, err := f.api(context.Background(), "GET", "/v1/me", "private-fixture-key", nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if reached.Load() {
		t.Fatal("redirect destination was contacted")
	}
}
