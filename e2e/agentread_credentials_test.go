//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Agent-selected reads go through a recording proxy to real SQLite. The proxy is a
// server this isolated machine knows, with both its person and seat credentials.
func TestAgentStatusAndAuditNeverUseThePersonsLogin(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	e := tm.admin
	e.run("pair", "general", "--name", "reader")
	e.run("board", "new", "other", "--server", tm.url())
	owner := tm.key(e)
	var stored struct {
		Agents []struct {
			Server, Board, Name, Token string
			MemberID                   string `json:"member_id"`
		}
	}
	credentialPath := filepath.Join(e.configDir(), "credentials.json")
	raw, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	seat := stored.Agents[0]
	upstream, err := url.Parse(tm.url())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(r *http.Request) { director(r); r.Host = upstream.Host }
	humanStream := make(chan struct{}, 1)
	var mu sync.Mutex
	ownerRequests, seatRequests, wrongBoard := 0, 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		switch r.Header.Get("Authorization") {
		case "Bearer " + owner:
			if r.URL.Path == "/v1/stream" {
				select {
				case humanStream <- struct{}{}:
				default:
				}
			} else {
				ownerRequests++
			}
		case "Bearer " + seat.Token:
			seatRequests++
		}
		if strings.HasPrefix(r.URL.Path, "/v1/boards/") && r.URL.Path != "/v1/boards/"+seat.Board && !strings.HasPrefix(r.URL.Path, "/v1/boards/"+seat.Board+"/") {
			wrongBoard = true
		}
		mu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { srv.CloseClientConnections(); srv.Close() })
	for i := range stored.Agents {
		stored.Agents[i].Server = srv.URL
	}
	// Encode with the credential file's snake-case names, retaining the permanent id.
	credentials := []map[string]string{}
	for _, c := range stored.Agents {
		credentials = append(credentials, map[string]string{"server": c.Server, "member_id": c.MemberID, "board": c.Board, "name": c.Name, "token": c.Token})
	}
	write := func(path string, v any) {
		t.Helper()
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(credentialPath, map[string]any{"agents": credentials})
	write(filepath.Join(e.configDir(), "servers.json"), map[string]any{"servers": []map[string]string{{"url": srv.URL, "key": owner, "handle": "sam"}}, "default": srv.URL})
	write(filepath.Join(e.dir, ".aboard"), map[string]any{"server": map[string]string{"name": srv.URL, "url": srv.URL}, "board": "other"})
	bound := e.claudeSession("credential-bound")
	bound.run("resume", "reader")
	// The trusted machine opens its person-authenticated shared stream before commands
	// run. Its stream auth is independent of the REST credential used by each command.
	select {
	case <-humanStream:
	case <-time.After(20 * time.Second):
		t.Fatal("daemon did not open its shared stream")
	}
	unbound := e.claudeSession("credential-unbound")
	for _, selection := range []struct {
		name       string
		vars, args []string
	}{
		{name: "flag", args: []string{"--as", "reader"}},
		{name: "environment", vars: []string{"ABOARD_AGENT=reader"}},
		{name: "session", vars: bound.vars},
	} {
		for _, command := range [][]string{{"status"}, {"audit", "verify"}} {
			t.Run(selection.name+"/"+command[0], func(t *testing.T) {
				mu.Lock()
				ownerRequests, seatRequests, wrongBoard = 0, 0, false
				mu.Unlock()
				args := append(append(append([]string{}, command...), selection.args...), "--json")
				result := e.exec(selection.vars, "", args...)
				if result.code != 0 {
					t.Fatalf("selected read failed: %s", result)
				}
				if field(t, result.json(t), "board") != seat.Board {
					t.Fatal("selected read used the folder's board")
				}
				mu.Lock()
				human, agent, wrong := ownerRequests, seatRequests, wrongBoard
				mu.Unlock()
				if human != 0 || agent == 0 || wrong {
					t.Fatalf("selected read: person requests %d, seat requests %d, wrong board %t", human, agent, wrong)
				}
			})
		}
	}
	for _, selection := range []struct {
		name       string
		vars, args []string
	}{
		{name: "unknown flag", args: []string{"--as", "missing"}},
		{name: "unknown environment", vars: []string{"ABOARD_AGENT=missing"}},
		{name: "unbound session", vars: unbound.vars},
	} {
		for _, command := range [][]string{{"status"}, {"audit", "verify"}} {
			t.Run(selection.name+"/"+command[0], func(t *testing.T) {
				mu.Lock()
				ownerRequests, seatRequests = 0, 0
				mu.Unlock()
				args := append(append(append([]string{}, command...), selection.args...), "--json")
				result := e.exec(selection.vars, "", args...)
				if selection.name == "unbound session" && command[0] == "status" {
					if result.code != 0 {
						t.Fatalf("unbound status lost diagnostics: %s", result)
					}
				} else if result.code != 1 || errorCode(t, result.json(t)) != "agent_not_selected" {
					t.Fatalf("unknown seat didn't refuse: %s", result)
				}
				mu.Lock()
				human, agent := ownerRequests, seatRequests
				mu.Unlock()
				if human != 0 || agent != 0 {
					t.Fatalf("unknown selection made authenticated REST requests: person %d, seat %d", human, agent)
				}
			})
		}
	}
	mu.Lock()
	ownerRequests, seatRequests = 0, 0
	mu.Unlock()
	e.run("status", "--json")
	e.run("audit", "verify", "--json")
	mu.Lock()
	human, agent := ownerRequests, seatRequests
	mu.Unlock()
	if human == 0 || agent != 0 {
		t.Fatalf("terminal control used wrong credential: person %d, seat %d", human, agent)
	}
	bound.run("join", "--board", "other")
	for _, selection := range []struct {
		name       string
		vars, args []string
	}{
		{name: "flag", vars: bound.vars, args: []string{"--as", "reader"}},
		{name: "environment", vars: append(append([]string{}, bound.vars...), "ABOARD_AGENT=reader")},
		{name: "session", vars: bound.vars},
	} {
		t.Run("multi-seat/"+selection.name, func(t *testing.T) {
			mu.Lock()
			ownerRequests, seatRequests, wrongBoard = 0, 0, false
			mu.Unlock()
			args := append(append([]string{"audit", "verify"}, selection.args...), "--json")
			result := e.exec(selection.vars, "", args...)
			if result.code != 1 || errorCode(t, result.json(t)) != "board_ambiguous" {
				t.Fatalf("ambiguous audit didn't refuse: %s", result)
			}
			mu.Lock()
			human, agent := ownerRequests, seatRequests
			mu.Unlock()
			if human != 0 || agent != 0 {
				t.Fatalf("ambiguous audit sent credentials: person %d, seat %d", human, agent)
			}
			result = e.exec(selection.vars, "", append(args, "--board", seat.Board)...)
			if result.code != 0 || field(t, result.json(t), "board") != seat.Board {
				t.Fatalf("qualified audit chose wrong seat: %s", result)
			}
			mu.Lock()
			human, agent, wrong := ownerRequests, seatRequests, wrongBoard
			mu.Unlock()
			if human != 0 || agent == 0 || wrong {
				t.Fatalf("qualified audit credential/board mismatch: person %d, seat %d, wrong board %t", human, agent, wrong)
			}
		})
	}
}
