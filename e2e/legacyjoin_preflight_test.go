//go:build e2e

package e2e

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func preflightExtension(t *testing.T, e *env, id string, caps []string) net.Conn {
	t.Helper()
	e.run("daemon", "start")
	conn, err := net.Dial("unix", e.socketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := map[string]any{"v": 1, "op": "hello", "harness": "omp", "session": id, "boot": "boot-" + id, "source": "startup", "process": map[string]any{"pid": os.Getpid()}, "cwd": e.dir, "capabilities": caps}
	if err := json.NewEncoder(conn).Encode(hello); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var welcome map[string]any
	if err := json.Unmarshal(line, &welcome); err != nil {
		t.Fatal(err)
	}
	if welcome["event"] != "welcome" {
		t.Fatalf("hello: %s", line)
	}
	go func() {
		for {
			if _, err := reader.ReadBytes('\n'); err != nil {
				return
			}
		}
	}()
	return conn
}

func preflightFiles(t *testing.T, e *env) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range []string{"credentials.json", "servers.json"} {
		raw, err := os.ReadFile(filepath.Join(e.configDir(), name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out[name] = string(raw)
	}
	return out
}

// The daemon is real; the proxy counts only resource-creating API calls, not the
// existing seat's presence or inbox traffic.
func TestLegacyExtensionRefusesNewSeatsBeforeAPIWrites(t *testing.T) {
	for _, mode := range []string{"unknown", "disconnected"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			first := e.run("pair", "writer-reviewer", "--json").json(t)
			second := e.run("pair", "writer-reviewer", "--new", "--json").json(t)
			originalAddr := e.addr
			target, _ := url.Parse("http://" + originalAddr)
			proxy := httputil.NewSingleHostReverseProxy(target)
			var writes atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && (r.URL.Path == "/v1/boards" || r.URL.Path == "/v1/join" || r.URL.Path == "/v1/guest-join" || r.URL.Path == "/v1/delegations" || r.URL.Path == "/v1/delegations/boards") {
					writes.Add(1)
				}
				r.Host = target.Host
				proxy.ServeHTTP(w, r)
			}))
			t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
			key, err := os.ReadFile(filepath.Join(e.configDir(), "local-owner-token"))
			if err != nil {
				t.Fatal(err)
			}
			saved, err := json.Marshal(map[string]any{"servers": []map[string]string{{"url": server.URL, "handle": "alex", "key": strings.TrimSpace(string(key))}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.configDir(), "servers.json"), saved, 0o600); err != nil {
				t.Fatal(err)
			}
			conn := preflightExtension(t, e, "legacy-preflight", []string{"future-v99"})
			s := &session{e: e, harness: "omp", id: "legacy-preflight", vars: []string{"ABOARD_SESSION=omp:legacy-preflight"}}
			s.run("join", "Join Aboard board first on "+strings.TrimPrefix(server.URL, "http://")+" as reviewer with code "+field(t, first, "join.code").(string), "--name", "first-seat")
			if mode == "disconnected" {
				_ = conn.Close()
			}
			before := preflightFiles(t, e)
			writes.Store(0)
			code := field(t, second, "join.code").(string)
			// The board in a pasted line is merely a hint. It cannot excuse redeeming a
			// code which actually grants a different board.
			wrongHint := "Join Aboard board " + field(t, first, "board.name").(string) + " on " + strings.TrimPrefix(server.URL, "http://") + " as reviewer with code " + code
			for _, args := range [][]string{{"join", code}, {"join", wrongHint}} {
				r := e.exec(s.vars, "", append(args, "--json")...)
				if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "extension_outdated") {
					t.Fatalf("%v must refuse before creating resources:\n%s", args, r)
				}
				if writes.Load() != 0 {
					t.Fatalf("%v made %d API writes", args, writes.Load())
				}
				after := preflightFiles(t, e)
				for name, raw := range before {
					if after[name] != raw {
						t.Fatalf("%v changed %s", args, name)
					}
				}
			}
			// Implicit creation follows this session's bound server rather than the
			// directory's server. Its extension must still support another seat.
			r := e.exec(s.vars, "", "pair", "--new", "--json")
			if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "extension_outdated") {
				t.Fatalf("pair without current extension capability:\n%s", r)
			}
			if writes.Load() != 0 {
				t.Fatalf("refused pair made %d API writes", writes.Load())
			}
			after := preflightFiles(t, e)
			for name, raw := range before {
				if after[name] != raw {
					t.Fatalf("refused pair changed %s", name)
				}
			}
			var foreignCalls atomic.Int64
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreignCalls.Add(1); w.WriteHeader(500) }))
			defer foreign.Close()
			guest := "Join Aboard board foreign on " + strings.TrimPrefix(foreign.URL, "http://") + " as guest with code " + code
			r = e.exec(s.vars, "", "join", guest, "--json")
			if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "session_on_another_server") || foreignCalls.Load() != 0 {
				t.Fatalf("cross-server guest join: calls=%d\n%s", foreignCalls.Load(), r)
			}
		})
	}
}

func TestPairingRequiresCurrentExtensionOnlyAfterFirstSeat(t *testing.T) {
	for _, current := range []bool{false, true} {
		name := "legacy"
		if current {
			name = "current"
		}
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			caps := []string{"future-v99"}
			if current {
				caps = []string{"handoff-v1"}
			}
			preflightExtension(t, e, "pair-preflight", caps)
			s := &session{e: e, harness: "omp", id: "pair-preflight", vars: []string{"ABOARD_SESSION=omp:pair-preflight"}}
			// An old extension still supports the first seat, preserving the quickstart.
			first := s.run("pair", "writer-reviewer", "--json").json(t)
			if field(t, first, "agent.name") == nil {
				t.Fatal("first pair didn't create a seat")
			}
			before := preflightFiles(t, e)
			boardsBefore := e.run("boards", "--json").json(t)["boards"].([]any)
			r := e.exec(s.vars, "", "pair", "writer-reviewer", "--new")
			if current {
				if r.code != 0 || !strings.Contains(r.stdout, "Use --board ") {
					t.Fatalf("current extension second pair:\n%s", r)
				}
				st := s.run("status", "--json").json(t)
				if seats, ok := st["seats"].([]any); !ok || len(seats) != 2 {
					t.Fatalf("second pair seats: %v", st)
				}
			} else {
				if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "extension_outdated") {
					t.Fatalf("old extension second pair:\n%s", r)
				}
				if after := e.run("boards", "--json").json(t)["boards"].([]any); len(after) != len(boardsBefore) {
					t.Fatal("refused pair created a board")
				}
				after := preflightFiles(t, e)
				for name, raw := range before {
					if after[name] != raw {
						t.Fatalf("refused pair changed %s", name)
					}
				}
			}
		})
	}
}

func TestCurrentExtensionRedeemsCodeForSecondSeat(t *testing.T) {
	e := newEnv(t)
	first := e.run("pair", "writer-reviewer", "--json").json(t)
	second := e.run("pair", "writer-reviewer", "--new", "--json").json(t)
	preflightExtension(t, e, "current-code", []string{"handoff-v1"})
	s := &session{e: e, harness: "omp", id: "current-code", vars: []string{"ABOARD_SESSION=omp:current-code"}}
	s.run("join", field(t, first, "join.code").(string), "--name", "first")
	out := s.run("join", field(t, second, "join.code").(string), "--name", "second")
	if !strings.Contains(out.stdout, "Use --board "+field(t, second, "board.name").(string)) {
		t.Fatalf("second code reminder:\n%s", out)
	}
	if st := s.run("status", "--json").json(t); len(st["seats"].([]any)) != 2 {
		t.Fatalf("second code seats: %v", st)
	}
}

// Observe the daemon's disconnect before releasing the guest grant. This is an
// external transport barrier, not a fake daemon or a timer-dependent assertion.
func awaitPreflightDisconnect(t *testing.T, e *env, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", e.socketPath())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(deadline)
		err = json.NewEncoder(conn).Encode(map[string]any{"v": 1, "op": "agents", "harness": "omp", "session": id})
		var response struct {
			Capabilities []string `json:"capabilities"`
		}
		if err == nil {
			err = json.NewDecoder(conn).Decode(&response)
		}
		_ = conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Capabilities) == 0 {
			return
		}
	}
	t.Fatal("daemon kept the disconnected extension's capabilities")
}

func TestGuestGrantIsNotSavedAfterExtensionDisconnects(t *testing.T) {
	e := newEnv(t)
	first := e.run("pair", "writer-reviewer", "--json").json(t)
	guest := e.run("invite", "--guest", "visitor", "--json").json(t)
	target, _ := url.Parse("http://" + e.addr)
	proxy := httputil.NewSingleHostReverseProxy(target)
	// The proxy's handler runs on server goroutines, so the extension's connection is
	// published to it atomically once the test has made it.
	var conn atomic.Pointer[net.Conn]
	var guestWrites atomic.Int64
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.Request.URL.Path == "/v1/guest-join" && r.StatusCode == http.StatusCreated {
			if c := conn.Load(); c != nil {
				_ = (*c).Close()
			}
			awaitPreflightDisconnect(t, e, "guest-preflight")
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/guest-join" {
			guestWrites.Add(1)
		}
		r.Host = target.Host
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	key, err := os.ReadFile(filepath.Join(e.configDir(), "local-owner-token"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(map[string]any{"servers": []map[string]string{{"url": server.URL, "handle": "alex", "key": strings.TrimSpace(string(key))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.configDir(), "servers.json"), saved, 0o600); err != nil {
		t.Fatal(err)
	}
	c := preflightExtension(t, e, "guest-preflight", []string{"handoff-v1"})
	conn.Store(&c)
	s := &session{e: e, harness: "omp", id: "guest-preflight", vars: []string{"ABOARD_SESSION=omp:guest-preflight"}}
	s.run("join", "Join Aboard board first on "+strings.TrimPrefix(server.URL, "http://")+" as reviewer with code "+field(t, first, "join.code").(string), "--name", "first")
	// The agent remains bound, but this machine has no person key for the proxy URL,
	// so this line takes the public anonymous guest redemption path.
	if err := os.Remove(filepath.Join(e.configDir(), "servers.json")); err != nil {
		t.Fatal(err)
	}
	before := preflightFiles(t, e)
	words := strings.Fields(field(t, guest, "join_line").(string))
	line := "Join Aboard board first on " + strings.TrimPrefix(server.URL, "http://") + " as guest with code " + words[len(words)-1]
	r := e.exec(s.vars, "", "join", line, "--json")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "extension_outdated") || guestWrites.Load() != 1 {
		t.Fatalf("guest grant after disconnect: writes=%d\n%s", guestWrites.Load(), r)
	}
	after := preflightFiles(t, e)
	for name, raw := range before {
		if after[name] != raw {
			t.Fatalf("guest grant after disconnect saved %s", name)
		}
	}
}
