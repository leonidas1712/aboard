//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func TestPersonStatusShowsBoardsAndOnlyOwnLocations(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	first := tm.newBoard(tm.admin, "open")
	second := tm.newBoard(tm.admin, "private")
	own := tm.agentToken(tm.admin, first)
	folder := "/tmp/status-own-project"
	status, _ := tm.call("PUT", "/v1/me/location", own, map[string]any{"harness": "claude-code", "session_id": "status-own-session", "folder": folder})
	if status != http.StatusOK {
		t.Fatalf("own location: %d", status)
	}
	other := tm.person("sam")
	tm.admin.run("board", "add", "@sam", "--board", first)
	foreign := tm.agentToken(other, first)
	status, _ = tm.call("PUT", "/v1/me/location", foreign, map[string]any{"harness": "codex", "session_id": "private-other-session", "folder": "/tmp/private-other-project"})
	if status != http.StatusOK {
		t.Fatalf("other location: %d", status)
	}
	tm.admin.run("board", "new", "status-archived")
	tm.admin.run("board", "archive", "status-archived")
	for _, json := range []bool{false, true} {
		args := []string{"status"}
		if json {
			args = append(args, "--json")
		}
		got := tm.admin.run(args...)
		if strings.Contains(got.stdout, "private-other-") || strings.Contains(got.stdout, "status-archived") {
			t.Fatalf("overview leaked/archived: %s", got)
		}
		if json {
			out := got.json(t)
			matchesCLISpec(t, "StatusOutput", out)
			boards := field(t, out, "boards").([]any)
			if len(boards) != 2 || out["board"] != nil || out["agent"] != nil {
				t.Fatalf("overview: %v", out)
			}
			if !strings.Contains(got.stdout, folder) {
				t.Fatalf("missing own location: %v", out)
			}
		} else {
			if strings.Count(got.stdout, "Server:") != 1 ||
				!strings.Contains(got.stdout, "Server: local ("+tm.admin.addr+") running") {
				t.Errorf("overview must name its server once: %s", got)
			}
			if strings.Count(got.stdout, "policy starter") != 2 ||
				strings.Count(got.stdout, "tighten with aboard board policy recommended") != 1 {
				t.Errorf("overview must retain badges and share its starter hint: %s", got)
			}
			for _, want := range []string{"Server:", "Daemon:", "Setup:", first, second, "unread", folder, "starter"} {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("missing %q: %s", want, got)
				}
			}
		}
	}
	narrowed := tm.admin.run("status", "--board", second, "--json").json(t)
	if field(t, narrowed, "board") != second || narrowed["boards"] != nil {
		t.Fatalf("--board failed to narrow: %v", narrowed)
	}
	session := tm.admin.claudeSession("status-unbound")
	unbound := session.run("status", "--json").json(t)
	if unbound["boards"] != nil {
		t.Fatalf("unbound session used person login: %v", unbound)
	}
}

func TestPersonOverviewNamesItsDefaultServerOnce(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	person := tm.person("sam")
	person.run("up")
	person.run("servers", "name", tm.url(), "qa")
	person.run("servers", "use", "qa")
	person.run("board", "new", "first")
	person.run("board", "new", "second")
	text := person.run("status").stdout
	if strings.Count(text, "Server:") != 1 || !strings.Contains(text, "Server: qa ("+tm.admin.addr+") reachable") {
		t.Fatalf("overview must name its default server once: %s", text)
	}
	if strings.Count(text, "policy starter") != 2 || strings.Count(text, "tighten with aboard board policy recommended") != 1 {
		t.Fatalf("overview lost badges or repeated its starter hint: %s", text)
	}
}

func TestPersonOverviewTimeoutRetainsKnownBoards(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	tm.newBoard(tm.admin, "private")
	tm.newBoard(tm.admin, "private")
	target, err := url.Parse(tm.url())
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) { director(r); r.Host = target.Host }
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me/agents" {
			<-r.Context().Done()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(peer.Close)
	login := tm.admin.exec(nil, tm.key(tm.admin)+"\n", "login", peer.URL)
	if login.code != 0 {
		t.Fatalf("fixture proxy login: %s", login)
	}
	got := tm.admin.exec([]string{"ABOARD_E2E_STATUS_BUDGET="}, "", "status", "--server", peer.URL, "--json")
	out := got.json(t)
	if got.code != 1 || errorCode(t, out) != "server_unreachable" {
		t.Fatalf("overview timeout: %s", got)
	}
	partial := field(t, out, "error.details.status").(map[string]any)
	matchesCLISpec(t, "StatusOutput", partial)
	if boards, ok := partial["boards"].([]any); !ok || len(boards) != 2 {
		t.Fatalf("lost known board metadata: %v", partial)
	}
}
